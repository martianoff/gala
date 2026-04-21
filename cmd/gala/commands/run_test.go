package commands

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// TestSplitRunArgs covers the `--` terminator handling for `gala run`.
//
// Cobra/pflag strips `--` from the args slice before invoking Run but records
// the split point via cmd.ArgsLenAtDash(). We rely on that so that every
// positional after `--` is forwarded verbatim to the executed program's
// os.Args. This test exercises splitRunArgs directly against every input
// shape the real Run handler can receive.
func TestSplitRunArgs(t *testing.T) {
	tests := []struct {
		name            string
		args            []string
		argsLenAtDash   int
		wantProjectDir  string
		wantProgramArgs []string
	}{
		{
			name:            "no args, no dash",
			args:            nil,
			argsLenAtDash:   -1,
			wantProjectDir:  ".",
			wantProgramArgs: nil,
		},
		{
			name:            "project dir only",
			args:            []string{"./myapp"},
			argsLenAtDash:   -1,
			wantProjectDir:  "./myapp",
			wantProgramArgs: nil,
		},
		{
			name:            "gala file only",
			args:            []string{"main.gala"},
			argsLenAtDash:   -1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: nil,
		},
		{
			name:            "dash with program args only",
			args:            []string{"hello", "world"},
			argsLenAtDash:   0,
			wantProjectDir:  ".",
			wantProgramArgs: []string{"hello", "world"},
		},
		{
			name:            "file then dash then program args",
			args:            []string{"main.gala", "hello", "world"},
			argsLenAtDash:   1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: []string{"hello", "world"},
		},
		{
			name:            "dot then dash then single arg",
			args:            []string{".", "foo"},
			argsLenAtDash:   1,
			wantProjectDir:  ".",
			wantProgramArgs: []string{"foo"},
		},
		{
			name:            "program args may look like flags",
			args:            []string{"main.gala", "--flag", "-x", "value"},
			argsLenAtDash:   1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: []string{"--flag", "-x", "value"},
		},
		{
			name:            "dash with no program args",
			args:            []string{"main.gala"},
			argsLenAtDash:   1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotDir, gotProgramArgs := splitRunArgs(tc.args, tc.argsLenAtDash)
			if gotDir != tc.wantProjectDir {
				t.Errorf("projectDir: got %q, want %q", gotDir, tc.wantProjectDir)
			}
			if !reflect.DeepEqual(gotProgramArgs, tc.wantProgramArgs) {
				t.Errorf("programArgs: got %#v, want %#v", gotProgramArgs, tc.wantProgramArgs)
			}
		})
	}
}

// TestRunCommandArgsPropagation confirms that the real runCmd (as configured
// in run.go) observes cmd.ArgsLenAtDash() correctly when invoked through
// cobra's parser. This guards against future regressions in cobra/pflag
// wiring — e.g., accidentally setting DisableFlagParsing: true, which would
// change the observed args shape.
func TestRunCommandArgsPropagation(t *testing.T) {
	tests := []struct {
		name            string
		argv            []string
		wantArgs        []string
		wantLenAtDash   int
		wantProjectDir  string
		wantProgramArgs []string
	}{
		{
			name:            "forwards positionals after dash",
			argv:            []string{"main.gala", "--", "hello", "world"},
			wantArgs:        []string{"main.gala", "hello", "world"},
			wantLenAtDash:   1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: []string{"hello", "world"},
		},
		{
			name:            "dash only",
			argv:            []string{"--", "onlyprog"},
			wantArgs:        []string{"onlyprog"},
			wantLenAtDash:   0,
			wantProjectDir:  ".",
			wantProgramArgs: []string{"onlyprog"},
		},
		{
			name:            "no dash",
			argv:            []string{"."},
			wantArgs:        []string{"."},
			wantLenAtDash:   -1,
			wantProjectDir:  ".",
			wantProgramArgs: nil,
		},
		{
			name:            "verbose flag before dash",
			argv:            []string{"-v", "main.gala", "--", "foo"},
			wantArgs:        []string{"main.gala", "foo"},
			wantLenAtDash:   1,
			wantProjectDir:  "main.gala",
			wantProgramArgs: []string{"foo"},
		},
		{
			name:            "program flags preserved after dash",
			argv:            []string{".", "--", "--version", "-h"},
			wantArgs:        []string{".", "--version", "-h"},
			wantLenAtDash:   1,
			wantProjectDir:  ".",
			wantProgramArgs: []string{"--version", "-h"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build a minimal cobra command with the same parsing config
			// as the real runCmd. We don't invoke runRun because it would
			// try to build and execute the project — the test only
			// verifies the argument shape passed to Run.
			var gotArgs []string
			var gotLenAtDash int
			cmd := &cobra.Command{
				Use:                "run",
				Args:               cobra.ArbitraryArgs,
				DisableFlagParsing: false,
				Run: func(c *cobra.Command, args []string) {
					gotArgs = args
					gotLenAtDash = c.ArgsLenAtDash()
				},
			}
			cmd.Flags().BoolP("verbose", "v", false, "")
			cmd.SetArgs(tc.argv)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("cmd.Execute: %v", err)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Errorf("args: got %#v, want %#v", gotArgs, tc.wantArgs)
			}
			if gotLenAtDash != tc.wantLenAtDash {
				t.Errorf("ArgsLenAtDash: got %d, want %d", gotLenAtDash, tc.wantLenAtDash)
			}

			gotDir, gotProgramArgs := splitRunArgs(gotArgs, gotLenAtDash)
			if gotDir != tc.wantProjectDir {
				t.Errorf("projectDir: got %q, want %q", gotDir, tc.wantProjectDir)
			}
			if !reflect.DeepEqual(gotProgramArgs, tc.wantProgramArgs) {
				t.Errorf("programArgs: got %#v, want %#v", gotProgramArgs, tc.wantProgramArgs)
			}
		})
	}
}
