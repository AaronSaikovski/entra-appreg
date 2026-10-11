package cli

import (
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func init() {
	// Cobra would otherwise print a notice and exit when the binary is
	// started from Windows Explorer; this CLI handles that case itself.
	cobra.MousetrapHelpText = ""
}

const followingLongOption = "Expected a value, not a following long option."

// optionalString records a supplied value; a nil target means "not supplied".
// A later occurrence overrides an earlier one.
type optionalString struct{ target **string }

func (o optionalString) String() string {
	if *o.target == nil {
		return ""
	}
	return **o.target
}

func (o optionalString) Set(v string) error {
	// A long option never supplies a value; single-hyphen values are literal.
	if strings.HasPrefix(v, "--") {
		return errors.New(followingLongOption)
	}
	*o.target = &v
	return nil
}

func (optionalString) Type() string { return "string" }

// redirectList accumulates every raw --redirect-urls value in order.
type redirectList struct{ target *[]string }

func (r redirectList) String() string { return strings.Join(*r.target, ",") }

func (r redirectList) Set(v string) error {
	if strings.HasPrefix(v, "--") {
		return errors.New(followingLongOption)
	}
	*r.target = append(*r.target, v)
	return nil
}

func (redirectList) Type() string { return "stringArray" }

// rejectPositional is every subcommand's Args check: this CLI takes no
// positional arguments after the command word.
func rejectPositional(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return unknownArgument(args[0])
	}
	return nil
}

func unknownArgument(arg string) *UsageError {
	return Usagef("Unknown or incomplete argument: %s. Run with --help for usage.", arg)
}

// asUsageError returns err itself when it is a *UsageError, and otherwise
// wraps its text in the standard usage hint.
func asUsageError(err error) *UsageError {
	if usage, ok := errors.AsType[*UsageError](err); ok {
		return usage
	}
	return &UsageError{Message: strings.TrimSuffix(err.Error(), ".") + ". Run with --help for usage."}
}

// parseFlags parses args (without the command word) for the canonical
// command name with a fresh, silent Cobra tree. The tree holds only that one
// subcommand: the flags are persistent on the root so every command parses
// every option, and validate decides which ones the command accepts.
func parseFlags(commandName string, args []string) (*Options, error) {
	opts := &Options{}
	// Cobra's Execute returns nil without running the subcommand when it
	// handles a help flag itself. Parse intercepts --help first, so ran is a
	// defensive check that opts really comes from a completed parse.
	ran := false
	root := &cobra.Command{
		Use:               "entra-appreg",
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	root.AddCommand(&cobra.Command{
		Use:  commandName,
		Args: rejectPositional,
		RunE: func(*cobra.Command, []string) error {
			ran = true
			return nil
		},
	})

	flags := root.PersistentFlags()
	for _, f := range []struct {
		name   string
		target **string
	}{
		{"appid", &opts.AppID},
		{"name", &opts.Name},
		{"audience", &opts.Audience},
		{"scope-name", &opts.ScopeName},
		{"scope-display-name", &opts.ScopeDisplayName},
		{"scope-description", &opts.ScopeDescription},
		{"scope-consent", &opts.ScopeConsent},
		{"scope-user-display-name", &opts.ScopeUserDisplayName},
		{"scope-user-description", &opts.ScopeUserDescription},
		{"scope-state", &opts.ScopeState},
		{"top", &opts.Top},
		{"secret-expiry", &opts.SecretExpiry},
		{"secret-start", &opts.SecretStart},
		{"secret-end", &opts.SecretEnd},
		{"keyvault-uri", &opts.KeyVaultURI},
		{"keyvault-secret-name", &opts.KeyVaultSecretName},
		{"params-file", &opts.ParamsFile},
	} {
		flags.Var(optionalString{f.target}, f.name, "")
	}
	flags.Var(redirectList{&opts.RedirectURIs}, "redirect-urls", "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	flags.BoolVar(&opts.CreateSecret, "create-secret", false, "")
	// Replace Cobra's default help flag so it has no -h shorthand: Parse
	// handles -h/--help itself, and combined shorthands such as -hh must
	// stay unknown arguments instead of silently requesting help.
	flags.Bool("help", false, "")
	_ = flags.MarkHidden("help")

	root.SetGlobalNormalizationFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "redirect-url" {
			name = "redirect-urls"
		}
		return pflag.NormalizedName(name)
	})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return asUsageError(err)
	})
	root.SetArgs(append([]string{commandName}, args...))
	if err := root.Execute(); err != nil {
		return nil, asUsageError(err)
	}
	if !ran {
		return nil, &UsageError{Message: "Unknown or incomplete argument. Run with --help for usage."}
	}
	return opts, nil
}
