// Package cli parses entra-appreg command lines.
package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Command is the workflow selected on the command line.
type Command int

const (
	CommandCreate Command = iota
	CommandExposeAPI
	CommandList
	CommandDelete
)

// HelpTopic selects one of the help texts.
type HelpTopic int

const (
	TopicGeneral HelpTopic = iota
	TopicCreate
	TopicExposeAPI
	TopicList
	TopicDelete
)

// Kind tells the caller what a parsed invocation asks for.
type Kind int

const (
	KindRun Kind = iota
	KindHelp
	KindVersion
	KindNoArguments
)

// Invocation is the result of Parse. Topic is meaningful for KindHelp and
// Options only for KindRun.
type Invocation struct {
	Kind    Kind
	Topic   HelpTopic
	Options *Options
}

// Options holds the parsed options. A nil string pointer means the option was
// not supplied; a pointer to "" means it was supplied with an empty value.
type Options struct {
	Command Command

	AppID, Name, Audience, ScopeName, ScopeDisplayName, ScopeDescription, ScopeConsent,
	ScopeUserDisplayName, ScopeUserDescription, ScopeState, Top,
	SecretExpiry, SecretStart, SecretEnd *string

	// RedirectURIs are split on ',', trimmed and non-empty, in their raw
	// spelling; they are not yet deduplicated.
	RedirectURIs      []string
	RedirectsSupplied bool

	JSON, CreateSecret bool

	// ListLimit is 50 by default and math.MaxInt32 for "--top all".
	ListLimit int
}

// UsageError reports invalid command-line input (exit code 2).
type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }

func usagef(format string, args ...any) *UsageError {
	return &UsageError{Message: fmt.Sprintf(format, args...)}
}

// EqualFoldASCII reports whether a and b are equal under byte-wise ASCII case
// folding; non-ASCII bytes must match exactly.
func EqualFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// Parse parses the arguments after the program name. Every returned error is
// a *UsageError.
func Parse(args []string) (Invocation, error) {
	// Early exits happen before flag parsing: unknown commands fail first,
	// then version beats help anywhere (even in value positions).
	commandGiven := len(args) > 0 && !strings.HasPrefix(args[0], "-")
	command := CommandCreate
	commandName := "create"
	if commandGiven {
		word := strings.ToLower(args[0])
		switch word {
		case "help":
			topic := "general"
			if len(args) > 1 {
				topic = strings.ToLower(args[1])
			}
			switch topic {
			case "general":
				return Invocation{Kind: KindHelp, Topic: TopicGeneral}, nil
			case "create":
				return Invocation{Kind: KindHelp, Topic: TopicCreate}, nil
			case "expose-api":
				return Invocation{Kind: KindHelp, Topic: TopicExposeAPI}, nil
			case "list":
				return Invocation{Kind: KindHelp, Topic: TopicList}, nil
			case "delete":
				return Invocation{Kind: KindHelp, Topic: TopicDelete}, nil
			default:
				return Invocation{}, usagef("No help for '%s'. Try: help, help create, help expose-api, help list, help delete.", topic)
			}
		case "create":
			command = CommandCreate
		case "expose-api":
			command = CommandExposeAPI
		case "list":
			command = CommandList
		case "delete":
			command = CommandDelete
		default:
			return Invocation{}, usagef("Unknown command: %s. Use 'create', 'expose-api', 'list', 'delete' or 'help' (see --help).", word)
		}
		commandName = word
		args = args[1:]
	}
	for _, arg := range args {
		if arg == "--version" {
			return Invocation{Kind: KindVersion}, nil
		}
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			topic := TopicGeneral
			if commandGiven {
				switch command {
				case CommandCreate:
					topic = TopicCreate
				case CommandExposeAPI:
					topic = TopicExposeAPI
				case CommandList:
					topic = TopicList
				case CommandDelete:
					topic = TopicDelete
				}
			}
			return Invocation{Kind: KindHelp, Topic: topic}, nil
		}
	}
	if len(args) == 0 && !commandGiven {
		return Invocation{Kind: KindNoArguments}, nil
	}
	// pflag accepts --option=value and the -- terminator; this CLI does not.
	for _, arg := range args {
		if arg == "--" || (strings.HasPrefix(arg, "--") && strings.Contains(arg, "=")) {
			return Invocation{}, usagef("Unknown or incomplete argument: %s. Run with --help for usage.", arg)
		}
	}

	options, err := parseFlags(commandName, args)
	if err != nil {
		return Invocation{}, err
	}
	options.Command = command
	options.ListLimit = 50
	if err := validate(options); err != nil {
		return Invocation{}, err
	}
	// Creation fields and scope/secret semantics are deliberately checked
	// only after authentication and lookup, so an existing ID remains a no-op.
	return Invocation{Kind: KindRun, Options: options}, nil
}

type namedOption struct {
	name     string
	supplied bool
}

func suppliedNames(groups ...[]namedOption) []string {
	var names []string
	for _, group := range groups {
		for _, option := range group {
			if option.supplied {
				names = append(names, option.name)
			}
		}
	}
	return names
}

func validate(o *Options) error {
	o.RedirectsSupplied = len(o.RedirectURIs) > 0
	raw := o.RedirectURIs
	o.RedirectURIs = nil
	for _, value := range raw {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				o.RedirectURIs = append(o.RedirectURIs, part)
			}
		}
	}
	if o.AppID != nil && strings.TrimSpace(*o.AppID) == "" {
		return usagef("--appid cannot be blank; omit it when creating a new application.")
	}
	secretOptions := []namedOption{
		{"--create-secret", o.CreateSecret},
		{"--secret-expiry", o.SecretExpiry != nil},
		{"--secret-start", o.SecretStart != nil},
		{"--secret-end", o.SecretEnd != nil},
	}
	scopeOptions := []namedOption{
		{"--scope-name", o.ScopeName != nil},
		{"--scope-display-name", o.ScopeDisplayName != nil},
		{"--scope-description", o.ScopeDescription != nil},
		{"--scope-consent", o.ScopeConsent != nil},
		{"--scope-user-display-name", o.ScopeUserDisplayName != nil},
		{"--scope-user-description", o.ScopeUserDescription != nil},
		{"--scope-state", o.ScopeState != nil},
	}
	listOptions := []namedOption{{"--top", o.Top != nil}, {"--json", o.JSON}}
	missingID := o.AppID == nil || *o.AppID == "no-id"
	if o.Command == CommandDelete {
		if missingID {
			return usagef("delete requires --appid <application object id or client id>.")
		}
		forbidden := suppliedNames([]namedOption{
			{"--name", o.Name != nil},
			{"--redirect-urls", o.RedirectsSupplied},
			{"--audience", o.Audience != nil},
		}, scopeOptions, secretOptions, listOptions)
		if len(forbidden) > 0 {
			return usagef("%s can't be used with the delete command.", strings.Join(forbidden, ", "))
		}
	}
	if o.Command == CommandExposeAPI {
		if missingID {
			return usagef("expose-api requires --appid <application object id or client id>.")
		}
		if o.ScopeName == nil || strings.TrimSpace(*o.ScopeName) == "" {
			return usagef("expose-api requires --scope-name.")
		}
		forbidden := suppliedNames([]namedOption{
			{"--name", o.Name != nil},
			{"--redirect-urls", o.RedirectsSupplied},
			{"--audience", o.Audience != nil},
		}, secretOptions, listOptions)
		if len(forbidden) > 0 {
			return usagef("%s can't be used with the expose-api command.", strings.Join(forbidden, ", "))
		}
	}
	if o.Command == CommandList {
		forbidden := suppliedNames([]namedOption{
			{"--appid", o.AppID != nil},
			{"--redirect-urls", o.RedirectsSupplied},
			{"--audience", o.Audience != nil},
		}, scopeOptions, secretOptions)
		if len(forbidden) > 0 {
			return usagef("%s can't be used with the list command.", strings.Join(forbidden, ", "))
		}
		if o.Name != nil && strings.TrimSpace(*o.Name) == "" {
			return usagef("--name cannot be blank.")
		}
		if o.Top != nil {
			trimmed := strings.TrimSpace(*o.Top)
			if EqualFoldASCII(trimmed, "all") {
				o.ListLimit = math.MaxInt32
			} else if n, err := strconv.ParseInt(trimmed, 10, 32); err == nil && n > 0 {
				o.ListLimit = int(n)
			} else {
				return usagef("--top must be a positive whole number or 'all', got: %s", *o.Top)
			}
		}
	} else if o.Top != nil || o.JSON {
		return usagef("%s can only be used with the list command.", strings.Join(suppliedNames(listOptions), ", "))
	}
	return nil
}

// asUsageError returns err itself when it is a *UsageError, and otherwise
// wraps its text in the standard usage hint.
func asUsageError(err error) *UsageError {
	var usage *UsageError
	if errors.As(err, &usage) {
		return usage
	}
	return &UsageError{Message: strings.TrimSuffix(err.Error(), ".") + ". Run with --help for usage."}
}
