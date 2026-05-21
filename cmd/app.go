package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"os"

	"github.com/spore-host/libs/catalog"
	"github.com/spf13/cobra"
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

func runAppList(cmd *cobra.Command, args []string) error {
	apps := catalog.List()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tDESCRIPTION\tGPU\tFAMILIES\tLICENSE")
	for _, app := range apps {
		gpu := "no"
		if app.GPU {
			gpu = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			app.Name,
			app.Description,
			gpu,
			strings.Join(app.InstanceFamilies, ", "),
			app.License,
		)
	}
	return w.Flush()
}

func init() {
	rootCmd.AddCommand(appCmd)
	appCmd.AddCommand(appListCmd)
}
