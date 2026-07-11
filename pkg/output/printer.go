package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fatih/color"
	"github.com/spore-host/libs/i18n"
	"github.com/spore-host/truffle/pkg/aws"
	"gopkg.in/yaml.v3"
)

// Printer handles output formatting
type Printer struct {
	useColor bool
}

// NewPrinter creates a new output printer
func NewPrinter(useColor bool) *Printer {
	return &Printer{useColor: useColor}
}

// table is a minimal table renderer that sizes columns to content width.
// We implement this directly because tablewriter v1.1.4 ignores
// WithHeaderAutoFormat(tw.Off) and still applies CamelCase splitting to headers.
type table struct {
	headers  []string
	rows     [][]string
	useColor bool
}

func newTable(headers []string, useColor bool) *table {
	return &table{headers: headers, useColor: useColor}
}

func (t *table) append(row []string) {
	t.rows = append(t.rows, row)
}

func (t *table) render() error {
	n := len(t.headers)

	// Column widths = max(header width, max data cell width) per column
	// Calculate widths BEFORE applying color to avoid counting ANSI escape sequences
	widths := make([]int, n)
	for i, h := range t.headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < n {
				// Strip ANSI color codes before measuring width
				cleanCell := stripAnsiCodes(cell)
				if w := utf8.RuneCountInString(cleanCell); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	// Build border lines
	borderLine := func(left, mid, right, horiz string) string {
		var sb strings.Builder
		sb.WriteString(left)
		for i, w := range widths {
			sb.WriteString(strings.Repeat(horiz, w+2))
			if i < n-1 {
				sb.WriteString(mid)
			}
		}
		sb.WriteString(right)
		return sb.String()
	}
	dataLine := func(cells []string) string {
		var sb strings.Builder
		sb.WriteString("│")
		for i := 0; i < n; i++ {
			var s string
			if i < len(cells) {
				s = cells[i]
			}
			// Calculate padding based on visual width (without ANSI codes)
			visualWidth := utf8.RuneCountInString(stripAnsiCodes(s))
			pad := widths[i] - visualWidth
			if pad < 0 {
				pad = 0
			}
			sb.WriteString(" ")
			sb.WriteString(s)
			sb.WriteString(strings.Repeat(" ", pad))
			sb.WriteString(" │")
		}
		return sb.String()
	}

	// Render header with optional color
	headers := make([]string, n)
	copy(headers, t.headers)
	if t.useColor {
		hdr := color.New(color.FgHiCyan, color.Bold).SprintFunc()
		for i, h := range headers {
			headers[i] = hdr(h)
		}
	}

	fmt.Fprintln(os.Stdout, borderLine("┌", "┬", "┐", "─"))
	fmt.Fprintln(os.Stdout, dataLine(headers))
	fmt.Fprintln(os.Stdout, borderLine("├", "┼", "┤", "─"))
	for _, row := range t.rows {
		fmt.Fprintln(os.Stdout, dataLine(row))
	}
	fmt.Fprintln(os.Stdout, borderLine("└", "┴", "┘", "─"))
	return nil
}

// PrintTable outputs results as a formatted table
func (p *Printer) PrintTable(results []aws.InstanceTypeResult, includeAZs bool, showPrice bool) error {
	// Check if any results have GPU info
	hasGPU := false
	// Show the nested-virt column only when at least one result supports it
	// (matches the conditional-GPU-columns convention).
	hasNestedV := false
	for _, r := range results {
		if r.GPUs > 0 {
			hasGPU = true
		}
		if r.NestedVirt {
			hasNestedV = true
		}
	}

	headers := []string{
		i18n.T("truffle.output.header.instance_type"),
		i18n.T("truffle.output.header.region"),
		i18n.T("truffle.output.header.vcpus"),
		i18n.T("truffle.output.header.memory"),
		i18n.T("truffle.output.header.architecture"),
	}
	if hasGPU {
		headers = append(headers, "GPUs", "GPU Model", "VRAM (GiB)")
	}
	if hasNestedV {
		headers = append(headers, "Nested-Virt")
	}
	if showPrice {
		headers = append(headers, "$/hr")
	}
	if includeAZs {
		headers = append(headers, i18n.T("truffle.output.header.availability_zones"))
	}

	table := newTable(headers, p.useColor)

	// Deduplicate: group by instanceType+region to collapse duplicate rows
	type resultKey struct{ instanceType, region string }
	seen := make(map[resultKey]bool)
	var deduped []aws.InstanceTypeResult
	for _, r := range results {
		k := resultKey{r.InstanceType, r.Region}
		if !seen[k] {
			seen[k] = true
			deduped = append(deduped, r)
		}
	}

	grouped := groupByInstanceType(deduped)

	for instanceType, regions := range grouped {
		for i, result := range regions {
			memGiB := fmt.Sprintf("%.1f", float64(result.MemoryMiB)/1024.0)

			// Add spawn support indicator to region
			regionDisplay := result.Region
			if result.SpawnSupported {
				if p.useColor {
					green := color.New(color.FgGreen)
					regionDisplay = result.Region + " " + green.Sprint("✓")
				} else {
					regionDisplay = result.Region + " ✓"
				}
			}

			row := []string{
				instanceType,
				regionDisplay,
				strconv.Itoa(int(result.VCPUs)),
				memGiB,
				result.Architecture,
			}
			if hasGPU {
				if result.GPUs > 0 {
					gpuModel := result.GPUModel
					if result.GPUManufacturer != "" {
						gpuModel = result.GPUManufacturer + " " + gpuModel
					}
					vramGiB := fmt.Sprintf("%.0f", float64(result.GPUMemoryMiB)/1024.0)
					row = append(row, strconv.Itoa(int(result.GPUs)), gpuModel, vramGiB)
				} else {
					row = append(row, "-", "-", "-")
				}
			}
			if hasNestedV {
				if result.NestedVirt {
					row = append(row, "✓")
				} else {
					row = append(row, "-")
				}
			}
			if showPrice {
				if result.OnDemandPrice > 0 {
					row = append(row, fmt.Sprintf("$%.4f", result.OnDemandPrice))
				} else {
					row = append(row, "N/A")
				}
			}
			if includeAZs {
				azs := strings.Join(result.AvailableAZs, ", ")
				if azs == "" {
					azs = "N/A"
				}
				// Limit AZ column to reasonable width
				if utf8.RuneCountInString(azs) > 40 {
					azs = truncateWithEllipsis(azs, 40)
				}
				row = append(row, azs)
			}
			if i > 0 {
				row[0] = ""
			}
			table.append(row)
		}
	}

	summaryMsg := i18n.Tf("truffle.output.summary.found", map[string]interface{}{
		"Count":   len(grouped),
		"Regions": countUniqueRegions(deduped),
	})
	if p.useColor {
		cyan := color.New(color.FgCyan, color.Bold)
		_, _ = cyan.Printf("\n%s %s\n\n", i18n.Emoji("magnifying_glass_tilted"), summaryMsg)
	} else {
		fmt.Printf("\n%s\n\n", summaryMsg)
	}

	if err := table.render(); err != nil {
		return err
	}

	// Add footer note about spawn support indicator
	// Check if any results have spawn support to decide whether to show the note
	hasSpawnSupported := false
	for _, r := range deduped {
		if r.SpawnSupported {
			hasSpawnSupported = true
			break
		}
	}
	if hasSpawnSupported {
		fmt.Println()
		if p.useColor {
			green := color.New(color.FgGreen)
			fmt.Printf("  %s = spawn-supported region\n", green.Sprint("✓"))
		} else {
			fmt.Println("  ✓ = spawn-supported region")
		}
	}

	// Note SageMaker (ml.*) results: they run on the underlying EC2 hardware but
	// are billed under the AmazonSageMaker offer, which truffle does not price
	// yet (issue #80) — so the $/hr column reads N/A by design, not by failure.
	hasSageMaker := false
	for _, r := range deduped {
		if r.Service == "sagemaker" {
			hasSageMaker = true
			break
		}
	}
	if hasSageMaker {
		fmt.Println()
		note := "  🤖 SageMaker ml.* types: specs shown from the underlying EC2 type; pricing not yet available"
		if p.useColor {
			fmt.Println(color.New(color.FgCyan).Sprint(note))
		} else {
			fmt.Println(note)
		}
	}

	return nil
}

// PrintJSON outputs results as JSON
func (p *Printer) PrintJSON(results []aws.InstanceTypeResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// PrintYAML outputs results as YAML
func (p *Printer) PrintYAML(results []aws.InstanceTypeResult) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer func() { _ = encoder.Close() }()
	return encoder.Encode(results)
}

// PrintCSV outputs results as CSV
func (p *Printer) PrintCSV(results []aws.InstanceTypeResult) error {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	// Include price column if any result has pricing populated
	hasPrice := false
	for _, r := range results {
		if r.OnDemandPrice > 0 {
			hasPrice = true
			break
		}
	}

	header := []string{"instance_type", "region", "vcpus", "memory_gib", "architecture"}
	if hasPrice {
		header = append(header, "on_demand_price")
	}
	header = append(header, "availability_zones")
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, result := range results {
		memGiB := fmt.Sprintf("%.1f", float64(result.MemoryMiB)/1024.0)
		azs := strings.Join(result.AvailableAZs, ";")
		row := []string{
			result.InstanceType,
			result.Region,
			strconv.Itoa(int(result.VCPUs)),
			memGiB,
			result.Architecture,
		}
		if hasPrice {
			row = append(row, fmt.Sprintf("%.4f", result.OnDemandPrice))
		}
		row = append(row, azs)
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// PrintSpotTable outputs Spot pricing results as a formatted table
func (p *Printer) PrintSpotTable(results []aws.SpotPriceResult) error {
	hasMultipleTimestamps := hasDistinctTimestamps(results)

	headers := []string{
		i18n.T("truffle.output.header.instance_type"),
		i18n.T("truffle.output.header.region"),
		i18n.T("truffle.output.header.availability_zone"),
		i18n.T("truffle.output.header.spot_price"),
		"On-Demand",
		"Savings",
	}
	if hasMultipleTimestamps {
		headers = append(headers, "Timestamp")
	}

	table := newTable(headers, p.useColor)

	grouped := make(map[string][]aws.SpotPriceResult)
	for _, result := range results {
		grouped[result.InstanceType] = append(grouped[result.InstanceType], result)
	}

	for instanceType, prices := range grouped {
		for i, result := range prices {
			odStr, savStr := "N/A", "N/A"
			if result.OnDemandPrice > 0 {
				odStr = fmt.Sprintf("$%.4f", result.OnDemandPrice)
				savStr = fmt.Sprintf("%.0f%%", result.SavingsPercent)
			}
			row := []string{
				instanceType,
				result.Region,
				result.AvailabilityZone,
				fmt.Sprintf("$%.4f", result.SpotPrice),
				odStr,
				savStr,
			}
			if hasMultipleTimestamps {
				row = append(row, formatTimestamp(result.Timestamp))
			}
			if i > 0 {
				row[0] = ""
			}
			table.append(row)
		}
	}

	return table.render()
}

func hasDistinctTimestamps(results []aws.SpotPriceResult) bool {
	if len(results) < 2 {
		return false
	}
	first := results[0].Timestamp
	for _, r := range results[1:] {
		if r.Timestamp != first {
			return true
		}
	}
	return false
}

func formatTimestamp(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("Jan 02 15:04")
}

// formatLocalWindow renders a start→end pair as a compact, local-timezone window
// for the Capacity Block tables. Capacity Blocks are returned in UTC (and all end
// at 11:30 UTC), so showing two full ISO-8601 UTC strings per row is both noisy
// and hard to relate to the operator's clock. We convert to the local zone and,
// when the window stays within one local day, drop the redundant date on the end:
//
//	Jun 18 04:30 → 11:30 PDT          (same local day)
//	Jun 18 04:30 → Jun 19 04:30 PDT   (spans local days)
//
// On a parse failure we fall back to the raw start string so nothing is hidden.
func formatLocalWindow(start, end string) string {
	st, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return start
	}
	st = st.Local()
	et, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return st.Format("Jan 02 15:04 MST")
	}
	et = et.Local()
	if st.YearDay() == et.YearDay() && st.Year() == et.Year() {
		return fmt.Sprintf("%s → %s %s", st.Format("Jan 02 15:04"), et.Format("15:04"), et.Format("MST"))
	}
	return fmt.Sprintf("%s → %s %s", st.Format("Jan 02 15:04"), et.Format("Jan 02 15:04"), et.Format("MST"))
}

// PrintSpotJSON outputs Spot pricing results as JSON
func (p *Printer) PrintSpotJSON(results []aws.SpotPriceResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// PrintSpotYAML outputs Spot pricing results as YAML
func (p *Printer) PrintSpotYAML(results []aws.SpotPriceResult) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer func() { _ = encoder.Close() }()
	return encoder.Encode(results)
}

// PrintSpotCSV outputs Spot pricing results as CSV
func (p *Printer) PrintSpotCSV(results []aws.SpotPriceResult) error {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	header := []string{"instance_type", "region", "availability_zone", "spot_price", "on_demand_price", "savings_percent", "timestamp"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, result := range results {
		row := []string{
			result.InstanceType,
			result.Region,
			result.AvailabilityZone,
			fmt.Sprintf("%.4f", result.SpotPrice),
			fmt.Sprintf("%.4f", result.OnDemandPrice),
			fmt.Sprintf("%.2f", result.SavingsPercent),
			result.Timestamp,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// PrintCapacityTable outputs capacity reservation results as a formatted table
func (p *Printer) PrintCapacityTable(results []aws.CapacityReservationResult) error {
	headers := []string{
		i18n.T("truffle.output.header.instance_type"),
		i18n.T("truffle.output.header.region"),
		i18n.T("truffle.output.header.az"),
		i18n.T("truffle.output.header.total"),
		i18n.T("truffle.output.header.available"),
		i18n.T("truffle.output.header.used"),
		i18n.T("truffle.output.header.state"),
		i18n.T("truffle.output.header.reservation_id"),
	}

	table := newTable(headers, p.useColor)

	for _, r := range results {
		utilizationPct := 0.0
		if r.TotalCapacity > 0 {
			utilizationPct = float64(r.UsedCapacity) / float64(r.TotalCapacity) * 100
		}
		row := []string{
			r.InstanceType,
			r.Region,
			r.AvailabilityZone,
			fmt.Sprintf("%d", r.TotalCapacity),
			fmt.Sprintf("%d", r.AvailableCapacity),
			fmt.Sprintf("%d (%.0f%%)", r.UsedCapacity, utilizationPct),
			r.State,
			shortenReservationID(r.ReservationID),
		}
		table.append(row)
	}

	return table.render()
}

// PrintCapacityJSON outputs capacity reservation results as JSON
func (p *Printer) PrintCapacityJSON(results []aws.CapacityReservationResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// PrintCapacityYAML outputs capacity reservation results as YAML
func (p *Printer) PrintCapacityYAML(results []aws.CapacityReservationResult) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer func() { _ = encoder.Close() }()
	return encoder.Encode(results)
}

// PrintCapacityCSV outputs capacity reservation results as CSV
func (p *Printer) PrintCapacityCSV(results []aws.CapacityReservationResult) error {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()

	header := []string{"instance_type", "region", "availability_zone", "total_capacity", "available_capacity", "used_capacity", "state", "reservation_id", "end_date", "platform"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, r := range results {
		row := []string{
			r.InstanceType,
			r.Region,
			r.AvailabilityZone,
			fmt.Sprintf("%d", r.TotalCapacity),
			fmt.Sprintf("%d", r.AvailableCapacity),
			fmt.Sprintf("%d", r.UsedCapacity),
			r.State,
			r.ReservationID,
			r.EndDate,
			r.Platform,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// --- Capacity Block offerings (purchasable; truffle#67) ---

// PrintBlockOfferingsTable renders purchasable Capacity Block offerings.
func (p *Printer) PrintBlockOfferingsTable(results []aws.CapacityBlockOfferingResult) error {
	headers := []string{"OFFERING ID", "TYPE", "COUNT", "AZ", "WINDOW (LOCAL)", "HOURS", "UPFRONT", "CCY"}
	table := newTable(headers, p.useColor)
	for _, r := range results {
		table.append([]string{
			r.OfferingID,
			r.InstanceType,
			fmt.Sprintf("%d", r.InstanceCount),
			r.AvailabilityZone,
			formatLocalWindow(r.StartDate, r.EndDate),
			fmt.Sprintf("%d", r.DurationHours),
			r.UpfrontFee,
			r.CurrencyCode,
		})
	}
	return table.render()
}

// PrintBlockOfferingsJSON renders offerings as JSON.
func (p *Printer) PrintBlockOfferingsJSON(results []aws.CapacityBlockOfferingResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// PrintBlockOfferingsYAML renders offerings as YAML.
func (p *Printer) PrintBlockOfferingsYAML(results []aws.CapacityBlockOfferingResult) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer func() { _ = encoder.Close() }()
	return encoder.Encode(results)
}

// PrintBlockOfferingsCSV renders offerings as CSV.
func (p *Printer) PrintBlockOfferingsCSV(results []aws.CapacityBlockOfferingResult) error {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()
	header := []string{"offering_id", "instance_type", "instance_count", "availability_zone", "region", "start_date", "end_date", "duration_hours", "upfront_fee", "currency_code", "tenancy"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, r := range results {
		row := []string{
			r.OfferingID, r.InstanceType, fmt.Sprintf("%d", r.InstanceCount),
			r.AvailabilityZone, r.Region, r.StartDate, r.EndDate,
			fmt.Sprintf("%d", r.DurationHours), r.UpfrontFee, r.CurrencyCode, r.Tenancy,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// --- Owned Capacity Blocks (existing reservations; truffle#67 --blocks) ---

// PrintBlocksTable renders owned/scheduled Capacity Block reservations.
func (p *Printer) PrintBlocksTable(results []aws.CapacityBlockResult) error {
	headers := []string{"BLOCK ID", "TYPE", "COUNT", "AZ", "WINDOW (LOCAL)", "HOURS", "STATE"}
	table := newTable(headers, p.useColor)
	for _, r := range results {
		table.append([]string{
			shortenReservationID(r.CapacityBlockID),
			r.InstanceType,
			fmt.Sprintf("%d", r.InstanceCount),
			r.AvailabilityZone,
			formatLocalWindow(r.StartDate, r.EndDate),
			fmt.Sprintf("%d", r.DurationHours),
			r.State,
		})
	}
	return table.render()
}

// PrintBlocksJSON renders owned Capacity Blocks as JSON.
func (p *Printer) PrintBlocksJSON(results []aws.CapacityBlockResult) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(results)
}

// PrintBlocksYAML renders owned Capacity Blocks as YAML.
func (p *Printer) PrintBlocksYAML(results []aws.CapacityBlockResult) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	defer func() { _ = encoder.Close() }()
	return encoder.Encode(results)
}

// PrintBlocksCSV renders owned Capacity Blocks as CSV.
func (p *Printer) PrintBlocksCSV(results []aws.CapacityBlockResult) error {
	writer := csv.NewWriter(os.Stdout)
	defer writer.Flush()
	header := []string{"capacity_block_id", "instance_type", "instance_count", "availability_zone", "start_date", "end_date", "duration_hours", "state"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, r := range results {
		row := []string{
			r.CapacityBlockID, r.InstanceType, fmt.Sprintf("%d", r.InstanceCount),
			r.AvailabilityZone, r.StartDate, r.EndDate,
			fmt.Sprintf("%d", r.DurationHours), r.State,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return nil
}

func groupByInstanceType(results []aws.InstanceTypeResult) map[string][]aws.InstanceTypeResult {
	grouped := make(map[string][]aws.InstanceTypeResult)
	for _, result := range results {
		grouped[result.InstanceType] = append(grouped[result.InstanceType], result)
	}
	return grouped
}

func countUniqueRegions(results []aws.InstanceTypeResult) int {
	regions := make(map[string]bool)
	for _, result := range results {
		regions[result.Region] = true
	}
	return len(regions)
}

func shortenReservationID(id string) string {
	if len(id) > 10 {
		return id[:10] + "..."
	}
	return id
}

// stripAnsiCodes removes ANSI escape sequences from a string for accurate width calculation
func stripAnsiCodes(s string) string {
	// ANSI escape sequences start with ESC[ and end with a letter
	// Simple regex: \x1b\[[0-9;]*[a-zA-Z]
	const ansiEscape = "\x1b"
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if i < len(s)-1 && s[i] == ansiEscape[0] && s[i+1] == '[' {
			// Skip until we find the ending letter
			i += 2
			for i < len(s) && !((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
				i++
			}
			if i < len(s) {
				i++ // Skip the ending letter
			}
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

// truncateWithEllipsis truncates a string to maxLen runes, adding "..." if truncated.
// It tries to break at a comma+space boundary to avoid cutting mid-word.
func truncateWithEllipsis(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}

	// Reserve 3 characters for "..."
	maxLen -= 3

	// Try to find a comma+space boundary near the limit
	runes := []rune(s)
	if maxLen > 0 && maxLen < len(runes) {
		// Look backwards from maxLen for ", "
		for i := maxLen; i > maxLen-10 && i > 0; i-- {
			if i < len(runes)-1 && runes[i] == ',' && runes[i+1] == ' ' {
				return string(runes[:i]) + "..."
			}
		}
		// No good break found, just truncate
		return string(runes[:maxLen]) + "..."
	}

	return s
}
