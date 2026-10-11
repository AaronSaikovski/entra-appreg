// Package cli parses entra-appreg command lines.
package cli

import (
	"fmt"
	"math"
	"slices"
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

// Error returns the message unchanged.
func (e *UsageError) Error() string { return e.Message }

// Usagef returns a *UsageError with a fmt.Sprintf-formatted message.
func Usagef(format string, args ...any) *UsageError {
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

// commandSpec ties a command word to its workflow and help topic.
type commandSpec struct {
	command Command
	topic   HelpTopic
}

// commands is keyed by the lowercased command word.
var commands = map[string]commandSpec{
	"create":     {CommandCreate, TopicCreate},
	"expose-api": {CommandExposeAPI, TopicExposeAPI},
	"list":       {CommandList, TopicList},
	"delete":     {CommandDelete, TopicDelete},
}

// Parse parses the arguments after the program name. Every returned error is
// a *UsageError.
//
// It runs in three phases: a pre-parse that resolves the command word and the
// help/version/no-arguments early exits, a Cobra pass that reads the flags,
// and validate, which enforces the per-command option rules. Creation fields
// and scope/secret semantics are deliberately left to app.create, which
// checks them after the --appid lookup so an existing ID remains a no-op.
func Parse(args []string) (Invocation, error) {
	commandGiven := len(args) > 0 && !strings.HasPrefix(args[0], "-")
	name := "create"
	if commandGiven {
		name = strings.ToLower(args[0])
		args = args[1:]
		if name == "help" {
			return helpInvocation(args)
		}
	}
	// Unknown commands fail before anything else, including --version.
	spec, ok := commands[name]
	if !ok {
		return Invocation{}, Usagef("Unknown command: %s. Use 'create', 'expose-api', 'list', 'delete' or 'help' (see --help).", name)
	}
	if inv, ok := earlyExit(args, commandGiven, spec.topic); ok {
		return inv, nil
	}
	if err := rejectPflagOnlySyntax(args); err != nil {
		return Invocation{}, err
	}

	options, err := parseFlags(name, args)
	if err != nil {
		return Invocation{}, err
	}

	options.Command = spec.command
	options.ListLimit = 50
	if err := validate(options); err != nil {
		return Invocation{}, err
	}
	return Invocation{Kind: KindRun, Options: options}, nil
}

// helpInvocation resolves "help [topic]"; args follow the help word. Words
// after the topic are ignored.
func helpInvocation(args []string) (Invocation, error) {
	word := "general"
	if len(args) > 0 {
		word = strings.ToLower(args[0])
	}
	if word == "general" {
		return Invocation{Kind: KindHelp, Topic: TopicGeneral}, nil
	}
	spec, ok := commands[word]
	if !ok {
		return Invocation{}, Usagef("No help for '%s'. Try: help, help create, help expose-api, help list, help delete.", word)
	}
	return Invocation{Kind: KindHelp, Topic: spec.topic}, nil
}

// earlyExit returns the version, help or no-arguments invocation that args
// ask for, if any. It scans raw words before flag parsing, so --version beats
// --help anywhere and both win even in a value position or next to invalid
// options. Help without an explicit command word is the general topic.
func earlyExit(args []string, commandGiven bool, topic HelpTopic) (Invocation, bool) {
	if slices.Contains(args, "--version") {
		return Invocation{Kind: KindVersion}, true
	}
	if slices.ContainsFunc(args, func(arg string) bool { return arg == "-h" || arg == "--help" }) {
		if !commandGiven {
			topic = TopicGeneral
		}
		return Invocation{Kind: KindHelp, Topic: topic}, true
	}
	if len(args) == 0 && !commandGiven {
		return Invocation{Kind: KindNoArguments}, true
	}
	return Invocation{}, false
}

// rejectPflagOnlySyntax rejects the "--" terminator and "--option=value",
// which pflag accepts but this CLI does not.
func rejectPflagOnlySyntax(args []string) error {
	for _, arg := range args {
		if arg == "--" || (strings.HasPrefix(arg, "--") && strings.Contains(arg, "=")) {
			return unknownArgument(arg)
		}
	}
	return nil
}

// validate normalizes the redirect values and enforces the per-command option
// rules. The rule order decides which message wins when several apply, so it
// is part of the output contract.
func validate(o *Options) error {
	o.RedirectsSupplied = len(o.RedirectURIs) > 0
	o.RedirectURIs = splitRedirects(o.RedirectURIs)
	if o.AppID != nil && strings.TrimSpace(*o.AppID) == "" {
		return Usagef("--appid cannot be blank; omit it when creating a new application.")
	}
	missingID := o.AppID == nil || *o.AppID == "no-id"
	switch o.Command {
	case CommandDelete:
		if missingID {
			return Usagef("delete requires --appid <application object id or client id>.")
		}
		return rejectOutside(o, "delete", groupAppID)
	case CommandExposeAPI:
		if missingID {
			return Usagef("expose-api requires --appid <application object id or client id>.")
		}
		if o.ScopeName == nil || strings.TrimSpace(*o.ScopeName) == "" {
			return Usagef("expose-api requires --scope-name.")
		}
		return rejectOutside(o, "expose-api", groupAppID|groupScope)
	case CommandList:
		if err := rejectOutside(o, "list", groupName|groupList); err != nil {
			return err
		}
		if o.Name != nil && strings.TrimSpace(*o.Name) == "" {
			return Usagef("--name cannot be blank.")
		}
		if o.Top != nil {
			limit, err := parseTop(*o.Top)
			if err != nil {
				return err
			}
			o.ListLimit = limit
		}
		return nil
	default:
		if listOnly := suppliedNames(o, groupList); len(listOnly) > 0 {
			return Usagef("%s can only be used with the list command.", strings.Join(listOnly, ", "))
		}
		return nil
	}
}

// splitRedirects splits each raw --redirect-urls value on ',' and keeps the
// trimmed, non-empty parts in order. Deduplication happens in app.create.
func splitRedirects(raw []string) []string {
	var uris []string
	for _, value := range raw {
		for part := range strings.SplitSeq(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				uris = append(uris, part)
			}
		}
	}
	return uris
}

// parseTop returns the --top limit: a positive int32, or math.MaxInt32 for
// "all" in any ASCII case. Surrounding spaces are ignored.
func parseTop(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if EqualFoldASCII(trimmed, "all") {
		return math.MaxInt32, nil
	}
	if n, err := strconv.ParseInt(trimmed, 10, 32); err == nil && n > 0 {
		return int(n), nil
	}
	return 0, Usagef("--top must be a positive whole number or 'all', got: %s", raw)
}

// optionGroup is a bit set of option families; each command accepts a fixed
// set of families and rejects options from the others.
type optionGroup uint8

const (
	groupAppID optionGroup = 1 << iota
	groupName
	groupApplication // --redirect-urls and --audience: create only
	groupScope
	groupSecret
	groupList
)

// suppliedNames returns the supplied options that belong to groups. Names come
// out in one fixed order, not argument order, so grouped messages are stable.
func suppliedNames(o *Options, groups optionGroup) []string {
	options := [...]struct {
		name     string
		group    optionGroup
		supplied bool
	}{
		{"--appid", groupAppID, o.AppID != nil},
		{"--name", groupName, o.Name != nil},
		{"--redirect-urls", groupApplication, o.RedirectsSupplied},
		{"--audience", groupApplication, o.Audience != nil},
		{"--scope-name", groupScope, o.ScopeName != nil},
		{"--scope-display-name", groupScope, o.ScopeDisplayName != nil},
		{"--scope-description", groupScope, o.ScopeDescription != nil},
		{"--scope-consent", groupScope, o.ScopeConsent != nil},
		{"--scope-user-display-name", groupScope, o.ScopeUserDisplayName != nil},
		{"--scope-user-description", groupScope, o.ScopeUserDescription != nil},
		{"--scope-state", groupScope, o.ScopeState != nil},
		{"--create-secret", groupSecret, o.CreateSecret},
		{"--secret-expiry", groupSecret, o.SecretExpiry != nil},
		{"--secret-start", groupSecret, o.SecretStart != nil},
		{"--secret-end", groupSecret, o.SecretEnd != nil},
		{"--top", groupList, o.Top != nil},
		{"--json", groupList, o.JSON},
	}
	var names []string
	for _, option := range options {
		if option.supplied && option.group&groups != 0 {
			names = append(names, option.name)
		}
	}
	return names
}

// rejectOutside fails when any supplied option falls outside the groups the
// command accepts. Even an empty value counts as supplied.
func rejectOutside(o *Options, command string, allowed optionGroup) error {
	if names := suppliedNames(o, ^allowed); len(names) > 0 {
		return Usagef("%s can't be used with the %s command.", strings.Join(names, ", "), command)
	}
	return nil
}
