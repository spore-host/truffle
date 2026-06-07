package cmd

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spore-host/libs/catalog"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "Browse the spore.host application catalog",
	Long:  `List and inspect streamable research applications available in the spore.host catalog.`,
}

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all streamable applications in the catalog",
	RunE:  runAppList,
}

type appRow struct {
	Name             string   `json:"name" yaml:"name"`
	Description      string   `json:"description" yaml:"description"`
	GPU              bool     `json:"gpu" yaml:"gpu"`
	InstanceFamilies []string `json:"instance_families" yaml:"instance_families"`
	License          string   `json:"license" yaml:"license"`
}

func runAppList(cmd *cobra.Command, args []string) error {
	apps := catalog.List()

	rows := make([]appRow, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, appRow{
			Name:             app.Name,
			Description:      app.Description,
			GPU:              app.GPU,
			InstanceFamilies: app.InstanceFamilies,
			License:          app.License,
		})
	}

	switch outputFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "yaml":
		return yaml.NewEncoder(os.Stdout).Encode(rows)
	case "csv":
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"name", "description", "gpu", "instance_families", "license"})
		for _, r := range rows {
			gpu := "false"
			if r.GPU {
				gpu = "true"
			}
			_ = w.Write([]string{r.Name, r.Description, gpu, strings.Join(r.InstanceFamilies, ";"), r.License})
		}
		w.Flush()
		return w.Error()
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION\tGPU\tFAMILIES\tLICENSE")
		for _, r := range rows {
			gpu := "no"
			if r.GPU {
				gpu = "yes"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				r.Name, r.Description, gpu,
				strings.Join(r.InstanceFamilies, ", "), r.License)
		}
		return w.Flush()
	}
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appListCmd)
}
