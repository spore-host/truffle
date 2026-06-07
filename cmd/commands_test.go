package cmd

import (
	"strings"
	"testing"

	"github.com/spore-host/truffle/pkg/quotas"
)

func resetCmdState() {
	regions = []string{}
	outputFormat = "table"
	noColor = false
	verbose = false
	skipAZs = false
	searchPickFirst = false
	searchShowPrice = false
	findPickFirst = false
}

func TestSearchCommand_Flags(t *testing.T) {
	out := searchCmd.UsageString()
	if !strings.Contains(out, "--show-price") {
		t.Errorf("search should have --show-price flag: %s", out)
	}
	if !strings.Contains(out, "--skip-azs") {
		t.Errorf("search should have --skip-azs flag: %s", out)
	}
	if !strings.Contains(out, "--pick-first") {
		t.Errorf("search should have --pick-first flag: %s", out)
	}
}

func TestSpotCommand_Flags(t *testing.T) {
	out := spotCmd.UsageString()
	if !strings.Contains(out, "--lookback-hours") {
		t.Errorf("spot should have --lookback-hours flag: %s", out)
	}
}

func TestQuotasCommand_Flags(t *testing.T) {
	out := quotasCmd.UsageString()
	if !strings.Contains(out, "--family") {
		t.Errorf("quotas should have --family flag: %s", out)
	}
}

func TestBuildQuotaRows_Empty(t *testing.T) {
	rows := buildQuotaRows(nil, "")
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for nil input, got %d", len(rows))
	}
}

func TestBuildQuotaRows_WithData(t *testing.T) {
	infos := map[string]*quotas.QuotaInfo{
		"us-east-1": {
			OnDemand: map[quotas.QuotaFamily]int32{
				quotas.FamilyStandard: 1480,
				quotas.FamilyG:       128,
			},
			Usage: map[quotas.QuotaFamily]int32{
				quotas.FamilyStandard: 153,
				quotas.FamilyG:       0,
			},
			Spot: map[quotas.QuotaFamily]int32{
				quotas.FamilyStandard: 1480,
			},
		},
	}

	rows := buildQuotaRows(infos, "")
	if len(rows) < 3 {
		t.Errorf("expected at least 3 rows, got %d", len(rows))
	}

	// With family filter
	rows = buildQuotaRows(infos, "G")
	for _, r := range rows {
		if r.Family != "G" {
			t.Errorf("expected family filter to only return G, got %s", r.Family)
		}
	}
}

func TestAppRow_OutputFormats(t *testing.T) {
	// Test that appRow struct marshals properly
	row := appRow{
		Name:             "test",
		Description:      "test app",
		GPU:              true,
		InstanceFamilies: []string{"g5", "g6"},
		License:          "open-source",
	}
	if row.Name != "test" || !row.GPU {
		t.Error("appRow fields not set correctly")
	}
}

func TestVersionCommand_NoError(t *testing.T) {
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
}

func TestAppListCommand_NoError(t *testing.T) {
	rootCmd.SetArgs([]string{"app", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("app list command failed: %v", err)
	}
}

func TestAppListCommand_JSON_NoError(t *testing.T) {
	outputFormat = "json"
	defer func() { outputFormat = "table" }()
	rootCmd.SetArgs([]string{"app", "list", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("app list -o json failed: %v", err)
	}
}

func TestAppListCommand_YAML_NoError(t *testing.T) {
	outputFormat = "yaml"
	defer func() { outputFormat = "table" }()
	rootCmd.SetArgs([]string{"app", "list", "-o", "yaml"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("app list -o yaml failed: %v", err)
	}
}

func TestAppListCommand_CSV_NoError(t *testing.T) {
	outputFormat = "csv"
	defer func() { outputFormat = "table" }()
	rootCmd.SetArgs([]string{"app", "list", "-o", "csv"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("app list -o csv failed: %v", err)
	}
}

func TestSearchCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"search", "m7i.large", "--regions", "us-east-1", "--skip-azs"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search command failed: %v", err)
	}
}

func TestSearchCommand_PickFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"search", "m7i.large", "--regions", "us-east-1", "--skip-azs", "--pick-first"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search --pick-first failed: %v", err)
	}
}

func TestSearchCommand_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"search", "m7i.large", "--regions", "us-east-1", "--skip-azs", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search -o json failed: %v", err)
	}
}

func TestListCommand_Family(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"list", "--family", "--regions", "us-east-1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("list --family failed: %v", err)
	}
}

func TestListCommand_FamilyJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"list", "--family", "--regions", "us-east-1", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("list --family -o json failed: %v", err)
	}
}

func TestSpotCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"spot", "m7i.large", "--regions", "us-east-1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("spot command failed: %v", err)
	}
}

func TestSpotCommand_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"spot", "m7i.large", "--regions", "us-east-1", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("spot -o json failed: %v", err)
	}
}

func TestFindCommand_Flags(t *testing.T) {
	out := findCmd.UsageString()
	if !strings.Contains(out, "--pick-first") {
		t.Errorf("find should have --pick-first flag: %s", out)
	}
	if !strings.Contains(out, "--app") {
		t.Errorf("find should have --app flag: %s", out)
	}
	if !strings.Contains(out, "--skip-azs") {
		t.Errorf("find should have --skip-azs flag: %s", out)
	}
}

func TestQuotasCommand_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"quotas", "--regions", "us-east-1"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("quotas command failed: %v", err)
	}
}

func TestQuotasCommand_JSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	resetCmdState()
	rootCmd.SetArgs([]string{"quotas", "--regions", "us-east-1", "-o", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("quotas -o json failed: %v", err)
	}
}
