use std::fs::{self, OpenOptions};
use std::io::{self, Write};
use std::path::{Path, PathBuf};

pub(crate) fn write_result_file(app_name: &str, contents: &str) -> io::Result<PathBuf> {
    write_result_file_in(&std::env::current_dir()?, app_name, contents)
}

fn write_result_file_in(directory: &Path, app_name: &str, contents: &str) -> io::Result<PathBuf> {
    let sanitized: String = app_name
        .trim_matches([' ', '.'])
        .chars()
        .map(|character| {
            if illegal_filename_character(character) {
                '_'
            } else if character == ' ' {
                '-'
            } else {
                character
            }
        })
        .collect();
    let trimmed = sanitized.trim_matches([' ', '.']);
    let name = if trimmed.is_empty() {
        "app-registration"
    } else {
        trimmed
    };
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }

    let mut suffix = 0_u64;
    loop {
        let filename = if suffix == 0 {
            format!("{name}.txt")
        } else {
            format!("{name}-{suffix}.txt")
        };
        let path = directory.join(filename);
        let file = match options.open(&path) {
            Ok(file) => file,
            Err(error) => {
                // create_new never follows symlinks. Some platforms report an
                // occupied directory as PermissionDenied rather than AlreadyExists.
                // symlink_metadata also recognizes dangling symlink collisions.
                let occupied = error.kind() == io::ErrorKind::AlreadyExists
                    || (matches!(
                        error.kind(),
                        io::ErrorKind::PermissionDenied | io::ErrorKind::IsADirectory
                    ) && fs::symlink_metadata(&path).is_ok());
                if !occupied {
                    return Err(error);
                }
                suffix = suffix.checked_add(1).ok_or_else(|| {
                    io::Error::new(
                        io::ErrorKind::AlreadyExists,
                        "Report filename space exhausted",
                    )
                })?;
                continue;
            }
        };
        // Once claimed, write/flush failures are genuine failures, not collisions.
        // Leave the owner-only file in place; never retry and hide a partial write.
        write_contents(file, contents)?;
        return Ok(path);
    }
}

fn write_contents(mut writer: impl Write, contents: &str) -> io::Result<()> {
    writer.write_all(contents.as_bytes())?;
    writer.flush()
}

fn illegal_filename_character(character: char) -> bool {
    #[cfg(windows)]
    {
        character <= '\u{1f}' || "<>:\"/\\|?*".contains(character)
    }
    #[cfg(not(windows))]
    {
        matches!(character, '\0' | '/')
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::File;
    use std::io::BufWriter;
    use std::sync::{Arc, Barrier};

    #[test]
    fn spaces_become_hyphens_without_overwriting_existing_reports() {
        let directory = tempfile::tempdir().unwrap();
        let first = write_result_file_in(directory.path(), "Test-App-Delete1", "original").unwrap();
        let second = write_result_file_in(directory.path(), "Test App Delete1", "new").unwrap();
        assert_eq!(first.file_name().unwrap(), "Test-App-Delete1.txt");
        assert_eq!(second.file_name().unwrap(), "Test-App-Delete1-1.txt");
        assert_eq!(fs::read_to_string(first).unwrap(), "original");
        assert_eq!(fs::read_to_string(second).unwrap(), "new");
    }

    #[test]
    fn occupied_file_and_directory_are_not_overwritten() {
        let directory = tempfile::tempdir().unwrap();
        fs::write(directory.path().join("example.txt"), "sentinel").unwrap();
        fs::create_dir(directory.path().join("example-1.txt")).unwrap();
        let path = write_result_file_in(directory.path(), "example", "fake secret").unwrap();
        assert_eq!(path, directory.path().join("example-2.txt"));
        assert!(path.is_absolute());
        assert_eq!(fs::read_to_string(path).unwrap(), "fake secret");
        assert_eq!(
            fs::read_to_string(directory.path().join("example.txt")).unwrap(),
            "sentinel"
        );
        assert!(directory.path().join("example-1.txt").is_dir());
    }

    #[cfg(unix)]
    #[test]
    fn live_and_dangling_symlinks_are_collisions_not_targets() {
        use std::os::unix::fs::symlink;
        let directory = tempfile::tempdir().unwrap();
        let target = directory.path().join("target");
        let missing = directory.path().join("missing");
        fs::write(&target, "sentinel").unwrap();
        symlink(&target, directory.path().join("example.txt")).unwrap();
        symlink(&missing, directory.path().join("example-1.txt")).unwrap();
        let path = write_result_file_in(directory.path(), "example", "fake secret").unwrap();
        assert_eq!(path, directory.path().join("example-2.txt"));
        assert_eq!(fs::read_to_string(target).unwrap(), "sentinel");
        assert!(!missing.exists());
        assert_eq!(
            fs::read_link(directory.path().join("example-1.txt")).unwrap(),
            missing
        );
    }

    #[test]
    fn concurrent_writers_each_claim_a_distinct_complete_report() {
        let directory = tempfile::tempdir().unwrap();
        let barrier = Arc::new(Barrier::new(8));
        let workers: Vec<_> = (0..8)
            .map(|number| {
                let directory = directory.path().to_owned();
                let barrier = Arc::clone(&barrier);
                std::thread::spawn(move || {
                    let contents = format!("fake secret {number}");
                    barrier.wait();
                    let path = write_result_file_in(&directory, "example", &contents).unwrap();
                    assert_eq!(fs::read_to_string(&path).unwrap(), contents);
                    path
                })
            })
            .collect();
        let paths: std::collections::HashSet<_> = workers
            .into_iter()
            .map(|worker| worker.join().unwrap())
            .collect();
        assert_eq!(paths.len(), 8);
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 8);
    }

    #[cfg(unix)]
    #[test]
    fn report_is_owner_only_from_creation() {
        use std::os::unix::fs::PermissionsExt;
        let directory = tempfile::tempdir().unwrap();
        let path = write_result_file_in(directory.path(), "example", "fake secret").unwrap();
        assert_eq!(
            fs::metadata(path).unwrap().permissions().mode() & 0o777,
            0o600
        );
    }

    #[test]
    fn sanitizes_platform_characters_and_trims_only_spaces_and_dots() {
        let directory = tempfile::tempdir().unwrap();
        let path = write_result_file_in(directory.path(), " .a/\0b. ", "report").unwrap();
        assert_eq!(path.file_name().unwrap(), "a__b.txt");
        let fallback = write_result_file_in(directory.path(), " . . ", "report").unwrap();
        assert_eq!(fallback.file_name().unwrap(), "app-registration.txt");
        let platform_name = "a\\b:c*?\"<>|";
        let path = write_result_file_in(directory.path(), platform_name, "report").unwrap();
        #[cfg(windows)]
        assert_eq!(path.file_name().unwrap(), "a_b_c______.txt");
        #[cfg(not(windows))]
        assert_eq!(path.file_name().unwrap(), "a\\b:c*?\"<>|.txt");
    }

    #[test]
    fn genuine_open_failures_propagate_without_creating_alternate_reports() {
        let directory = tempfile::tempdir().unwrap();
        assert!(write_result_file_in(directory.path(), &"x".repeat(1024), "secret").is_err());
        assert_eq!(fs::read_dir(directory.path()).unwrap().count(), 0);
        let missing_directory = directory.path().join("missing");
        assert!(write_result_file_in(&missing_directory, "example", "secret").is_err());
        assert!(!missing_directory.exists());
    }

    #[test]
    fn genuine_write_and_buffered_flush_failures_propagate() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("sentinel");
        fs::write(&path, "unchanged").unwrap();
        // A read-only descriptor fails even when tests run as an elevated user.
        assert!(write_contents(File::open(&path).unwrap(), "fake secret").is_err());
        // This small write fits the buffer; only the explicit flush touches the
        // read-only descriptor, proving that flush errors are not discarded.
        let buffered = BufWriter::with_capacity(1024, File::open(&path).unwrap());
        assert!(write_contents(buffered, "fake secret").is_err());
        assert_eq!(fs::read_to_string(path).unwrap(), "unchanged");
    }
}
