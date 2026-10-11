package cli

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

// parse calls Parse and fails the test if it returns an error that is not a
// *UsageError, since main maps only usage errors to exit code 2.
func parse(t *testing.T, words ...string) (Invocation, error) {
	t.Helper()
	inv, err := Parse(words)
	if err != nil {
		if _, ok := errors.AsType[*UsageError](err); !ok {
			t.Fatalf("%q: Parse returned a non-UsageError: %v", words, err)
		}
	}
	return inv, err
}

func mustRun(t *testing.T, words ...string) *Options {
	t.Helper()
	inv, err := parse(t, words...)
	if err != nil {
		t.Fatalf("%q: unexpected error: %v", words, err)
	}
	if inv.Kind != KindRun || inv.Options == nil {
		t.Fatalf("%q: expected a command, got %+v", words, inv)
	}
	return inv.Options
}

func expectErr(t *testing.T, words ...string) error {
	t.Helper()
	_, err := parse(t, words...)
	if err == nil {
		t.Fatalf("%q: expected an error", words)
	}
	return err
}

func expectErrMessage(t *testing.T, want string, words ...string) {
	t.Helper()
	if err := expectErr(t, words...); err.Error() != want {
		t.Fatalf("%q: got %q, want %q", words, err, want)
	}
}

func expectHelp(t *testing.T, topic HelpTopic, words ...string) {
	t.Helper()
	inv, err := parse(t, words...)
	if err != nil || inv.Kind != KindHelp || inv.Topic != topic {
		t.Fatalf("%q: expected help topic %d, got %+v, %v", words, topic, inv, err)
	}
}

func expectVersion(t *testing.T, words ...string) {
	t.Helper()
	inv, err := parse(t, words...)
	if err != nil || inv.Kind != KindVersion {
		t.Fatalf("%q: expected version, got %+v, %v", words, inv, err)
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestDeleteRequiresIDAndRejectsUnrelatedOptions(t *testing.T) {
	if got := mustRun(t, "DeLeTe", "--appid", "id").Command; got != CommandDelete {
		t.Fatalf("command = %v", got)
	}
	for _, args := range [][]string{
		{"delete"},
		{"delete", "--appid"},
		{"delete", "--appid", ""},
		{"delete", "--appid", " "},
		{"delete", "--appid", "no-id"},
		{"delete", "--appid", "id", "--yes"},
		{"delete", "--appid", "id", "--force"},
	} {
		expectErr(t, args...)
	}
	for _, option := range []string{
		"--name", "--redirect-url", "--redirect-urls", "--audience", "--scope-name",
		"--scope-display-name", "--scope-description", "--scope-consent",
		"--scope-user-display-name", "--scope-user-description", "--scope-state",
		"--top", "--secret-expiry", "--secret-start", "--secret-end",
	} {
		expectErr(t, "delete", "--appid", "id", option, "")
	}
	for _, option := range []string{"--json", "--create-secret"} {
		expectErr(t, "delete", "--appid", "id", option)
	}
	expectHelp(t, TopicDelete, "delete", "--help")
	expectHelp(t, TopicDelete, "help", "delete")
	expectVersion(t, "delete", "--help", "--version")
}

func TestDeleteErrorMessages(t *testing.T) {
	expectErrMessage(t, "delete requires --appid <application object id or client id>.", "delete")
	expectErrMessage(t, "--name, --scope-state, --secret-end, --json can't be used with the delete command.",
		"delete", "--appid", "id", "--name", "x", "--json", "--scope-state", "", "--secret-end", "")
}

func TestRepeatedScalarsOverrideButRedirectsAppendAndFlagsStaySet(t *testing.T) {
	o := mustRun(t, "create",
		"--appid", "old", "--appid", "new",
		"--name", "first", "--name", "",
		"--create-secret", "--create-secret",
		"--redirect-url", "https://a/First",
		"--redirect-urls", " , ",
		"--redirect-url", "https://a/Last",
	)
	if deref(o.AppID) != "new" || deref(o.Name) != "" || !o.CreateSecret || !o.RedirectsSupplied {
		t.Fatalf("unexpected options %+v", o)
	}
	if want := []string{"https://a/First", "https://a/Last"}; !reflect.DeepEqual(o.RedirectURIs, want) {
		t.Fatalf("redirects = %q", o.RedirectURIs)
	}
	o = mustRun(t, "list", "--top", "0", "--top", "2", "--json", "--json")
	if o.ListLimit != 2 || !o.JSON {
		t.Fatalf("unexpected options %+v", o)
	}
}

func TestCobraDoesNotBroadenSyntax(t *testing.T) {
	for _, args := range [][]string{
		{"--"},
		{"list", "--"},
		{"--name=value"},
		{"--name=x"},
		{"--redirect-url=a"},
		{"list", "--na", "prefix"},
		{"list", "-j"},
		{"-x"},
		{"-hh"},
		{"list", "-"},
		{"list", "help"},
		{"create", "extra"},
		{"create", "help"},
		{"create", "list"},
		{"list", "---name", "x"},
		{"list", "--json=true"},
		{"--create-secret=false"},
		{"list", "--name", "--unknown", "--name", "valid"},
		{"list", "--name", "valid", "extra"},
		{"list", "--completion"},
	} {
		expectErr(t, args...)
	}
	expectErrMessage(t, "Unknown or incomplete argument: --name=x. Run with --help for usage.", "--name=x")
	expectErrMessage(t, "Unknown or incomplete argument: extra. Run with --help for usage.", "create", "extra")
	for _, args := range [][]string{{"--name", "--oops"}, {"--redirect-url", "--oops"}} {
		if err := expectErr(t, args...); !strings.Contains(err.Error(), "Expected a value, not a following long option.") {
			t.Fatalf("%q: got %q", args, err)
		}
	}
	expectHelp(t, TopicGeneral, "--name=x", "--help")
	expectVersion(t, "--", "--version")
}

func TestAliasAccumulatesWithCanonical(t *testing.T) {
	o := mustRun(t, "--redirect-url", "a", "--redirect-urls", "b,c")
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(o.RedirectURIs, want) || !o.RedirectsSupplied {
		t.Fatalf("redirects = %q, supplied = %v", o.RedirectURIs, o.RedirectsSupplied)
	}
}

func TestSingleHyphensAndEqualsRemainLiteralOptionValues(t *testing.T) {
	for _, value := range []string{"-", "-preview", "-x=y", "name=value", ""} {
		if got := deref(mustRun(t, "--name", value).Name); got != value {
			t.Fatalf("%q: name = %v", value, got)
		}
	}
	expectErr(t, "--name", "--not-a-value")
}

func TestEarlyExitPrecedenceIgnoresInvalidOptionsAndValues(t *testing.T) {
	expectVersion(t, "--name", "--help", "--version")
	expectHelp(t, TopicGeneral, "--name", "-h")
	expectHelp(t, TopicList, "LiSt", "--unknown", "--help")
	expectErr(t, "unknown", "--version", "--help")
	expectErr(t, "help", "unknown", "--version")
	expectHelp(t, TopicCreate, "help", "CREATE", "--version")
	inv, err := parse(t)
	if err != nil || inv.Kind != KindNoArguments {
		t.Fatalf("got %+v, %v", inv, err)
	}
	expectErrMessage(t, "Unknown command: unknown. Use 'create', 'expose-api', 'list', 'delete' or 'help' (see --help).", "unknown")
	expectErrMessage(t, "No help for 'nope'. Try: help, help create, help expose-api, help list, help delete.", "help", "Nope")
}

func TestHelpTopicSelection(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  HelpTopic
	}{
		{[]string{"help"}, TopicGeneral},
		{[]string{"HELP", "General"}, TopicGeneral},
		{[]string{"help", "create"}, TopicCreate},
		{[]string{"help", "Expose-API"}, TopicExposeAPI},
		{[]string{"help", "list"}, TopicList},
		{[]string{"help", "delete"}, TopicDelete},
		{[]string{"help", "list", "extra"}, TopicList},
		{[]string{"--help"}, TopicGeneral},
		{[]string{"--name", "x", "-h"}, TopicGeneral},
		{[]string{"create", "-h"}, TopicCreate},
		{[]string{"expose-api", "--help"}, TopicExposeAPI},
		{[]string{"list", "--help"}, TopicList},
		{[]string{"delete", "-h"}, TopicDelete},
	} {
		t.Run(strings.Join(c.words, " "), func(t *testing.T) {
			expectHelp(t, c.want, c.words...)
		})
	}
}

func TestValuesPreserveRawSpellingAndRedirectsAccumulate(t *testing.T) {
	o := mustRun(t,
		"--name", "first", "--name", " second ",
		"--appid", "no-id",
		"--redirect-url", " https://a/A, ,https://a/a ",
		"--redirect-urls", "https://a/A",
		"--scope-description", "-single",
		"--secret-end", "",
	)
	if deref(o.Name) != " second " || deref(o.AppID) != "no-id" ||
		deref(o.ScopeDescription) != "-single" || deref(o.SecretEnd) != "" || !o.RedirectsSupplied {
		t.Fatalf("unexpected options %+v", o)
	}
	if want := []string{"https://a/A", "https://a/a", "https://a/A"}; !reflect.DeepEqual(o.RedirectURIs, want) {
		t.Fatalf("redirects = %q", o.RedirectURIs)
	}
	for _, words := range [][]string{
		{"--name"},
		{"--name", "--audience", "x"},
		{"--Name", "x"},
		{"--name=x"},
	} {
		expectErr(t, words...)
	}
}

func TestCreationSemanticsRemainDeferredButBlankIDsDoNot(t *testing.T) {
	if got := mustRun(t, "create").Command; got != CommandCreate {
		t.Fatalf("command = %v", got)
	}
	if got := deref(mustRun(t, "--appid", "existing").AppID); got != "existing" {
		t.Fatalf("appid = %v", got)
	}
	mustRun(t, "--name", "", "--audience", "invalid", "--scope-state", "invalid", "--secret-expiry", "invalid")
	expectErrMessage(t, "--appid cannot be blank; omit it when creating a new application.", "--appid", " \t")
	expectErr(t, "expose-api", "--appid", "no-id", "--scope-name", "read")
	if got := mustRun(t, "expose-api", "--appid", "NO-ID", "--scope-name", "read").Command; got != CommandExposeAPI {
		t.Fatalf("command = %v", got)
	}
	expectErrMessage(t, "expose-api requires --scope-name.", "expose-api", "--appid", "id", "--scope-name", " ")
}

func TestListTopObeysSignedIntegerBoundaries(t *testing.T) {
	for raw, want := range map[string]int{
		" all ":      math.MaxInt32,
		"ALL":        math.MaxInt32,
		"2147483647": math.MaxInt32,
		" +1 ":       1,
		"999":        999,
	} {
		if got := mustRun(t, "list", "--top", raw).ListLimit; got != want {
			t.Fatalf("%q: limit = %d, want %d", raw, got, want)
		}
	}
	if got := mustRun(t, "list").ListLimit; got != 50 {
		t.Fatalf("default limit = %d", got)
	}
	for _, raw := range []string{"", "0", "-1", "2147483648", "1.0", "1_000", "allx"} {
		expectErr(t, "list", "--top", raw)
	}
	expectErrMessage(t, "--top must be a positive whole number or 'all', got: 0", "list", "--top", "0")
	expectErrMessage(t, "--name cannot be blank.", "list", "--name", " ")
}

func TestForbiddenOptionGroupsRejectEvenEmptyValues(t *testing.T) {
	for _, option := range []string{
		"--appid", "--redirect-url", "--audience", "--scope-name", "--scope-display-name",
		"--scope-description", "--scope-consent", "--scope-user-display-name",
		"--scope-user-description", "--scope-state", "--secret-expiry", "--secret-start",
		"--secret-end",
	} {
		expectErr(t, "list", option, "")
	}
	expectErr(t, "list", "--create-secret")
	for _, option := range []string{
		"--name", "--redirect-url", "--audience", "--top", "--secret-expiry", "--secret-start", "--secret-end",
	} {
		expectErr(t, "expose-api", "--appid", "id", "--scope-name", "read", option, "")
	}
	for _, option := range []string{"--json", "--create-secret"} {
		expectErr(t, "expose-api", "--appid", "id", "--scope-name", "read", option)
	}
	expectErrMessage(t, "--json can only be used with the list command.", "create", "--json")
	expectErrMessage(t, "--top, --json can only be used with the list command.", "create", "--top", "", "--json")
	expectErrMessage(t, "--appid, --redirect-urls can't be used with the list command.", "list", "--appid", "x", "--redirect-url", "a")
}

// TestValidationOrderAndMessages pins which rule wins when several fail and
// that grouped messages list options in a fixed order, not argument order.
func TestValidationOrderAndMessages(t *testing.T) {
	for _, c := range []struct {
		name  string
		words []string
		want  string
	}{
		{
			"delete lists every forbidden group in order",
			[]string{"delete", "--appid", "id", "--json", "--top", "1", "--secret-end", "x", "--create-secret",
				"--scope-state", "x", "--scope-name", "x", "--audience", "x", "--redirect-url", "x", "--name", "x"},
			"--name, --redirect-urls, --audience, --scope-name, --scope-state, --create-secret, --secret-end, --top, --json can't be used with the delete command.",
		},
		{
			"expose-api lists every forbidden group in order",
			[]string{"expose-api", "--appid", "id", "--scope-name", "r", "--json", "--top", "1", "--secret-start", "s",
				"--create-secret", "--audience", "a", "--redirect-url", "r", "--name", "n"},
			"--name, --redirect-urls, --audience, --create-secret, --secret-start, --top, --json can't be used with the expose-api command.",
		},
		{
			"list lists every forbidden group in order",
			[]string{"list", "--secret-expiry", "1", "--scope-consent", "x", "--scope-name", "x", "--audience", "x",
				"--redirect-url", "x", "--appid", "x", "--name", "n", "--top", "1", "--json"},
			"--appid, --redirect-urls, --audience, --scope-name, --scope-consent, --secret-expiry can't be used with the list command.",
		},
		{
			"blank appid beats command rules",
			[]string{"delete", "--appid", " ", "--name", "x"},
			"--appid cannot be blank; omit it when creating a new application.",
		},
		{
			"delete missing appid beats forbidden options",
			[]string{"delete", "--name", "x"},
			"delete requires --appid <application object id or client id>.",
		},
		{
			"expose-api missing appid beats missing scope name",
			[]string{"expose-api", "--appid", "no-id", "--json"},
			"expose-api requires --appid <application object id or client id>.",
		},
		{
			"expose-api missing scope name beats forbidden options",
			[]string{"expose-api", "--appid", "id", "--json"},
			"expose-api requires --scope-name.",
		},
		{
			"list forbidden options beat blank name",
			[]string{"list", "--name", " ", "--appid", "x"},
			"--appid can't be used with the list command.",
		},
		{
			"list blank name beats invalid top",
			[]string{"list", "--name", " ", "--top", "0"},
			"--name cannot be blank.",
		},
		{
			"create lists list-only options in fixed order",
			[]string{"--create-secret", "--json", "--top", "x", "--name", "n"},
			"--top, --json can only be used with the list command.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			expectErrMessage(t, c.want, c.words...)
		})
	}
}

func TestHelpTextUsesBinaryNameAndGoCredential(t *testing.T) {
	for _, topic := range []HelpTopic{TopicGeneral, TopicCreate, TopicExposeAPI, TopicList, TopicDelete} {
		text := HelpText(topic, "1.2.3")
		if strings.Contains(text, "cargo") || strings.Contains(text, "AzureCliCredential") || strings.Contains(text, "NOTE") {
			t.Fatalf("topic %d still has Rust-specific text", topic)
		}
		if !strings.Contains(text, "  entra-appreg ") {
			t.Fatalf("topic %d lacks entra-appreg usage", topic)
		}
		if topic != TopicDelete && !strings.Contains(text, "AzureCLICredential") {
			t.Fatalf("topic %d lacks AzureCLICredential", topic)
		}
	}
	general := HelpText(TopicGeneral, "1.2.3")
	if !strings.HasPrefix(general, "entra-appreg 1.2.3 - manage ") ||
		!strings.Contains(general, "Azure SDK for Go (azidentity) AzureCLICredential") {
		t.Fatalf("general help heading/authentication text wrong")
	}
}

func TestEqualFoldASCII(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"AzureADMyOrg", "azureadmyorg", true},
		{"all", "ALL", true},
		{"all", "alll", false},
		{"é", "É", false},
		{"K", "\u212a", false},
	} {
		if got := EqualFoldASCII(c.a, c.b); got != c.want {
			t.Fatalf("EqualFoldASCII(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
