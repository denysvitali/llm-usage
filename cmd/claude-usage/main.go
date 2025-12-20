package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/denysvitali/claude-code-usage/internal/api"
	"github.com/denysvitali/claude-code-usage/internal/credentials"
	"github.com/denysvitali/claude-code-usage/internal/version"
)

const barWidth = 20

// Lipgloss styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212"))

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("99"))

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	percentStyle = lipgloss.NewStyle().
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	criticalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	barFullStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("42"))

	barEmptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	tokenStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Italic(true)
)

var rootCmd = &cobra.Command{
	Use:     "claude-usage",
	Short:   "Display Claude AI API usage statistics",
	Long:    `A CLI tool to display your Claude AI API usage statistics for Pro and Max subscriptions.`,
	Version: version.Version,
	RunE:    runUsage,
}

func init() {
	rootCmd.Flags().Bool("json", false, "Output in JSON format")
	rootCmd.Flags().Bool("waybar", false, "Output in Waybar JSON format")

	_ = viper.BindPFlag("json", rootCmd.Flags().Lookup("json"))
	_ = viper.BindPFlag("waybar", rootCmd.Flags().Lookup("waybar"))
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runUsage(cmd *cobra.Command, args []string) error {
	jsonOutput := viper.GetBool("json")
	waybarOutput := viper.GetBool("waybar")

	creds, err := credentials.Load()
	if err != nil {
		if waybarOutput {
			outputWaybarError(err.Error())
			return nil
		}
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	if creds.ClaudeAiOauth.IsExpired() {
		msg := "Token expired - run 'claude' to refresh"
		if waybarOutput {
			outputWaybarError(msg)
			return nil
		}
		return fmt.Errorf("%s", msg)
	}

	client := api.NewClient(creds.ClaudeAiOauth.AccessToken)
	usage, err := client.GetUsage()
	if err != nil {
		if waybarOutput {
			outputWaybarError(err.Error())
			return nil
		}
		return fmt.Errorf("failed to fetch usage: %w", err)
	}

	switch {
	case waybarOutput:
		outputWaybar(usage)
	case jsonOutput:
		return outputJSON(usage)
	default:
		outputPretty(usage, creds.ClaudeAiOauth.ExpiresIn())
	}

	return nil
}

func outputJSON(usage *api.UsageResponse) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(usage); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	return nil
}

// WaybarOutput represents the JSON format expected by waybar custom modules
type WaybarOutput struct {
	Text       string `json:"text"`
	Tooltip    string `json:"tooltip"`
	Class      string `json:"class"`
	Percentage int    `json:"percentage"`
}

func outputWaybar(usage *api.UsageResponse) {
	var maxUtil float64
	if usage.FiveHour != nil && usage.FiveHour.Utilization > maxUtil {
		maxUtil = usage.FiveHour.Utilization
	}
	if usage.SevenDay != nil && usage.SevenDay.Utilization > maxUtil {
		maxUtil = usage.SevenDay.Utilization
	}

	class := "normal"
	if maxUtil >= 90 {
		class = "critical"
	} else if maxUtil >= 75 {
		class = "warning"
	}

	var textParts []string
	if usage.FiveHour != nil {
		textParts = append(textParts, fmt.Sprintf("5h:%.0f%%", usage.FiveHour.Utilization))
	}
	if usage.SevenDay != nil {
		textParts = append(textParts, fmt.Sprintf("7d:%.0f%%", usage.SevenDay.Utilization))
	}
	text := strings.Join(textParts, " ")

	var tooltipLines []string
	tooltipLines = append(tooltipLines, "Claude Usage", "")

	if usage.FiveHour != nil {
		line := fmt.Sprintf("5-Hour: %.1f%%", usage.FiveHour.Utilization)
		if d := usage.FiveHour.TimeUntilReset(); d != nil {
			line += fmt.Sprintf(" (resets in %s)", formatDuration(*d))
		}
		tooltipLines = append(tooltipLines, line)
	}

	if usage.SevenDay != nil {
		line := fmt.Sprintf("7-Day: %.1f%%", usage.SevenDay.Utilization)
		if d := usage.SevenDay.TimeUntilReset(); d != nil {
			line += fmt.Sprintf(" (resets in %s)", formatDuration(*d))
		}
		tooltipLines = append(tooltipLines, line)
	}

	if usage.SevenDaySonnet != nil {
		line := fmt.Sprintf("7-Day Sonnet: %.1f%%", usage.SevenDaySonnet.Utilization)
		if d := usage.SevenDaySonnet.TimeUntilReset(); d != nil {
			line += fmt.Sprintf(" (resets in %s)", formatDuration(*d))
		}
		tooltipLines = append(tooltipLines, line)
	}

	if usage.SevenDayOpus != nil {
		line := fmt.Sprintf("7-Day Opus: %.1f%%", usage.SevenDayOpus.Utilization)
		if d := usage.SevenDayOpus.TimeUntilReset(); d != nil {
			line += fmt.Sprintf(" (resets in %s)", formatDuration(*d))
		}
		tooltipLines = append(tooltipLines, line)
	}

	output := WaybarOutput{
		Text:       text,
		Tooltip:    strings.Join(tooltipLines, "\n"),
		Class:      class,
		Percentage: int(maxUtil),
	}

	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding waybar output: %v\n", err)
		os.Exit(1)
	}
}

func outputWaybarError(msg string) {
	output := WaybarOutput{
		Text:       "Claude: Error",
		Tooltip:    msg,
		Class:      "error",
		Percentage: 0,
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding waybar error output: %v\n", err)
		os.Exit(1)
	}
}

func outputPretty(usage *api.UsageResponse, tokenExpiresIn time.Duration) {
	// Title
	fmt.Println(titleStyle.Render("Claude Usage (Pro/Max Subscription)"))
	fmt.Println(titleStyle.Render(strings.Repeat("─", 36)))
	fmt.Println()

	printUsageWindow("5-Hour Window", usage.FiveHour)
	fmt.Println()
	printUsageWindow("7-Day Window", usage.SevenDay)

	if usage.SevenDaySonnet != nil {
		fmt.Println()
		printUsageWindow("7-Day Sonnet", usage.SevenDaySonnet)
	}

	if usage.SevenDayOpus != nil {
		fmt.Println()
		printUsageWindow("7-Day Opus", usage.SevenDayOpus)
	}

	if usage.SevenDayOAuthApp != nil {
		fmt.Println()
		printUsageWindow("7-Day OAuth Apps", usage.SevenDayOAuthApp)
	}

	if usage.IguanaNecktie != nil {
		fmt.Println()
		printUsageWindow("Iguana Necktie", usage.IguanaNecktie)
	}

	if usage.ExtraUsage != nil && usage.ExtraUsage.IsEnabled {
		fmt.Println()
		printExtraUsage(usage.ExtraUsage)
	}

	fmt.Println()
	fmt.Println(tokenStyle.Render(fmt.Sprintf("Token expires: %s", formatDuration(tokenExpiresIn))))
}

func printExtraUsage(extra *api.ExtraUsage) {
	fmt.Println(headerStyle.Render("Extra Usage Credits:"))
	if extra.Utilization != nil {
		bar := renderProgressBar(*extra.Utilization)
		pct := formatPercentage(*extra.Utilization)
		fmt.Printf("  %s %s  %s\n", labelStyle.Render("Usage:"), bar, pct)
	}
	if extra.UsedCredits != nil && extra.MonthlyLimit != nil {
		credits := fmt.Sprintf("$%.2f / $%.2f", *extra.UsedCredits, *extra.MonthlyLimit)
		fmt.Printf("  %s %s\n", labelStyle.Render("Credits:"), valueStyle.Render(credits))
	}
}

func printUsageWindow(name string, window *api.UsageWindow) {
	fmt.Println(headerStyle.Render(name + ":"))

	if window == nil {
		bar := barEmptyStyle.Render(strings.Repeat("░", barWidth))
		fmt.Printf("  %s %s  %s\n", labelStyle.Render("Usage:"), bar, labelStyle.Render("N/A"))
		fmt.Printf("  %s %s\n", labelStyle.Render("Resets:"), labelStyle.Render("N/A"))
		return
	}

	bar := renderProgressBar(window.Utilization)
	pct := formatPercentage(window.Utilization)
	fmt.Printf("  %s %s  %s\n", labelStyle.Render("Usage:"), bar, pct)

	if resetDur := window.TimeUntilReset(); resetDur != nil {
		resetText := fmt.Sprintf("in %s", formatDuration(*resetDur))
		fmt.Printf("  %s %s\n", labelStyle.Render("Resets:"), valueStyle.Render(resetText))
	} else {
		fmt.Printf("  %s %s\n", labelStyle.Render("Resets:"), labelStyle.Render("N/A"))
	}
}

func formatPercentage(percentage float64) string {
	text := fmt.Sprintf("%.1f%%", percentage)
	switch {
	case percentage >= 90:
		return criticalStyle.Render(text)
	case percentage >= 75:
		return warningStyle.Render(text)
	default:
		return normalStyle.Render(text)
	}
}

func renderProgressBar(percentage float64) string {
	filled := int(percentage / 100 * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	var barStyle lipgloss.Style
	switch {
	case percentage >= 90:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	case percentage >= 75:
		barStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	default:
		barStyle = barFullStyle
	}

	filledBar := barStyle.Render(strings.Repeat("█", filled))
	emptyBar := barEmptyStyle.Render(strings.Repeat("░", barWidth-filled))

	return filledBar + emptyBar
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}

	return strings.Join(parts, " ")
}
