package tui

import (
	"reflect"
	"testing"

	"github.com/logico/sparkle-cli/internal/config"
)

func TestSlashCommandSuggestionsSorted(t *testing.T) {
	commands := map[string]config.SlashCommand{
		"fix":           {Template: "fix {{.Input}}"},
		"explain":       {Template: "explain {{.Input}}"},
		"generate-code": {Template: "generate {{.Input}}"},
	}

	got := slashCommandSuggestions(commands)
	want := []string{"/explain ", "/fix ", "/generate-code "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("slashCommandSuggestions() = %v, want %v", got, want)
	}
}

func TestExactSlashCommand(t *testing.T) {
	commands := map[string]config.SlashCommand{
		"fix": {Template: "fix {{.Input}}"},
	}

	tests := []struct {
		name      string
		input     string
		wantCmd   string
		wantRest  string
		wantMatch bool
	}{
		{name: "exact command", input: "/fix", wantCmd: "/fix", wantMatch: true},
		{name: "command with payload", input: "/fix ls -la", wantCmd: "/fix", wantRest: " ls -la", wantMatch: true},
		{name: "command with trailing space", input: "/fix ", wantCmd: "/fix", wantRest: " ", wantMatch: true},
		{name: "partial command", input: "/fi", wantMatch: false},
		{name: "unknown command", input: "/foo ls", wantMatch: false},
		{name: "plain text", input: "ls -la", wantMatch: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotCmd, gotRest, gotMatch := exactSlashCommand(test.input, commands)
			if gotCmd != test.wantCmd || gotRest != test.wantRest || gotMatch != test.wantMatch {
				t.Fatalf("exactSlashCommand(%q) = (%q, %q, %t), want (%q, %q, %t)", test.input, gotCmd, gotRest, gotMatch, test.wantCmd, test.wantRest, test.wantMatch)
			}
		})
	}
}

func TestRenderUserBlockContentOnlyColorsKnownSlashCommands(t *testing.T) {
	cfg := config.Config{Commands: map[string]config.SlashCommand{"fix": {Template: "fix {{.Input}}"}}}
	m := newModel(cfg, "")

	colored := m.renderUserBlockContent("/fix ls -la")
	wantColored := m.styles.slashCommand.Render("/fix") + " " + m.styles.userText.Render("ls -la")
	if colored != wantColored {
		t.Fatalf("renderUserBlockContent() = %q, want %q", colored, wantColored)
	}

	plain := m.renderUserBlockContent("/fi ls -la")
	wantPlain := m.styles.userText.Render("/fi ls -la")
	if plain != wantPlain {
		t.Fatalf("renderUserBlockContent() = %q, want %q", plain, wantPlain)
	}
}
