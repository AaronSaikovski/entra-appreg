package cli

// Version is the entra-appreg version reported by --version and the general
// help heading. CI smoke reads it, and the release workflow requires the pushed
// tag to equal "v" + Version.
const Version = "0.0.1"
