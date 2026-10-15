package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetAllFlags returns every flag in the command tree to its default, since
// pflag keeps parsed values across Execute calls.
func resetAllFlags(t *testing.T) {
	t.Helper()
	var walk func(c *cobra.Command)
	reset := func(f *pflag.Flag) {
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			_ = sv.Replace(nil)
		} else if err := f.Value.Set(f.DefValue); err != nil {
			t.Fatalf("reset --%s: %v", f.Name, err)
		}
		f.Changed = false
	}
	walk = func(c *cobra.Command) {
		c.Flags().VisitAll(reset)
		c.PersistentFlags().VisitAll(reset)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
}

// runCLI runs truffle with args the way main does, returning what it wrote
// to stderr (Execute's writer and cobra's own) and the error.
func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags(t)
	t.Cleanup(func() { resetAllFlags(t); rootCmd.SetArgs(nil); rootCmd.SetErr(nil); rootCmd.SetOut(nil) })
	var stderr, cobraOut bytes.Buffer
	rootCmd.SetErr(&cobraOut)
	rootCmd.SetOut(&cobraOut)
	rootCmd.SetArgs(args)
	err := executeRoot(&stderr)
	return cobraOut.String() + stderr.String(), err
}

// TestRuntimeErrorsPrintedOnceWithoutUsage is the gate for #218: an error
// from a command that started running is printed exactly once, with no usage
// text; a usage mistake still gets the usage text.
func TestRuntimeErrorsPrintedOnceWithoutUsage(t *testing.T) {
	runtime := []struct {
		name string
		args []string
		msg  string
	}{
		{"bad --service value", []string{"find", "graviton", "--service", "bogus"}, `unknown --service "bogus"`},
		{"conflicting flags", []string{"find", "graviton", "--local-zones", "--skip-azs"}, "--local-zones cannot be combined with --skip-azs"},
		{"bad --price-unit value", []string{"search", "m7i.large", "--price-unit", "fortnight"}, "fortnight"},
	}
	for _, tc := range runtime {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLI(t, tc.args...)
			if err == nil {
				t.Fatalf("truffle %v: want an error", tc.args)
			}
			if n := strings.Count(out, tc.msg); n != 1 {
				t.Errorf("error printed %d times, want once:\n%s", n, out)
			}
			if strings.Contains(out, "Usage:") {
				t.Errorf("runtime error followed by the usage text:\n%s", out)
			}
		})
	}

	usage := []struct {
		name string
		args []string
		msg  string
		hint string
	}{
		{"unknown flag", []string{"find", "graviton", "--no-such-flag"}, "unknown flag: --no-such-flag", "Usage:"},
		{"wrong arg count", []string{"search"}, "accepts 1 arg(s), received 0", "Usage:"},
		{"unknown command", []string{"no-such-command"}, `unknown command "no-such-command"`, "Run 'truffle --help' for usage."},
	}
	for _, tc := range usage {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCLI(t, tc.args...)
			if err == nil {
				t.Fatalf("truffle %v: want an error", tc.args)
			}
			if n := strings.Count(out, tc.msg); n != 1 {
				t.Errorf("error printed %d times, want once:\n%s", n, out)
			}
			if e, h := strings.Index(out, "Error: "), strings.Index(out, tc.hint); e < 0 || h < e {
				t.Errorf("want the error first, then %q:\n%s", tc.hint, out)
			}
			if n := strings.Count(out, tc.hint); n != 1 {
				t.Errorf("want %q once after a usage error, got %d:\n%s", tc.hint, n, out)
			}
		})
	}
}
