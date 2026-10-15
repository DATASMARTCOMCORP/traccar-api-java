package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const javaSources = "../../../src/main/java/org/traccar"

// TestCommandsMatchJava re-reads the protocol classes so the table can't drift.
func TestCommandsMatchJava(t *testing.T) {
	cmdSrc, err := os.ReadFile(filepath.Join(javaSources, "model", "Command.java"))
	if err != nil {
		t.Skip("Java sources not found:", err)
	}
	constRe := regexp.MustCompile(`public static final String (TYPE_[A-Z0-9_]+) = "([^"]+)";`)
	types := map[string]string{}
	for _, m := range constRe.FindAllStringSubmatch(string(cmdSrc), -1) {
		types[m[1]] = m[2]
	}
	callRe := regexp.MustCompile(`(?s)setSupported(Data|Text)Commands\((.*?)\);`)
	files, _ := filepath.Glob(filepath.Join(javaSources, "protocol", "*Protocol.java"))
	if len(files) < 250 {
		t.Fatalf("found %d protocol classes", len(files))
	}
	seen := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		name := strings.ToLower(strings.TrimSuffix(filepath.Base(f), "Protocol.java"))
		var data, text []string
		for _, m := range callRe.FindAllStringSubmatch(string(src), -1) {
			for _, arg := range strings.Split(m[2], ",") {
				if arg = strings.TrimSpace(arg); arg != "" {
					v := types[strings.TrimPrefix(arg, "Command.")]
					if m[1] == "Data" {
						data = append(data, v)
					} else {
						text = append(text, v)
					}
				}
			}
		}
		got := declaredCommands[name]
		if !slices.Equal(got.data, data) || !slices.Equal(got.text, text) {
			t.Errorf("%s: table %v/%v, Java %v/%v", name, got.data, got.text, data, text)
		}
		if data != nil || text != nil {
			seen++
		}
	}
	if seen != len(declaredCommands) {
		t.Errorf("table has %d protocols, Java declares commands in %d", len(declaredCommands), seen)
	}
}

func TestSupportedCommands(t *testing.T) {
	// Java: new HashSet<>(supportedDataCommands) + "custom" for SuntechProtocol.
	want := []string{"engineStop", "custom", "positionSingle", "alarmArm", "alarmDisarm", "outputControl", "engineResume", "rebootDevice"}
	if got := SupportedCommands("suntech", false); !slices.Equal(got, want) {
		t.Errorf("suntech data: %v, want %v", got, want)
	}
	if got := SupportedCommands("osmand", false); !slices.Equal(got, []string{"custom"}) {
		t.Errorf("osmand: %v", got)
	}
	if got := SupportedCommands("suntech", true); !slices.Equal(got, []string{"custom"}) {
		t.Errorf("suntech text: %v", got)
	}
}
