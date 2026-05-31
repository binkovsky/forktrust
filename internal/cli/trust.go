package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/binkovsky/forktrust/internal/config"
)

var trustCmd = &cobra.Command{
	Use:   "trust [path]",
	Short: "Mark a repo's .forktrustconfig as trusted (allows command hooks)",
	Long: `Command hooks (run: <shell>) in .forktrustconfig are arbitrary shell
execution. To prevent a malicious commit from silently injecting commands,
forktrust refuses to run them until you explicitly mark the config as trusted.

trust pins the current SHA-256 of .forktrustconfig. Any future change to the
config revokes trust until you re-run "forktrust trust".

  forktrust trust                    # trust the cwd's repo
  forktrust trust /path/to/repo      # trust a specific path
  forktrust trust --list             # show all trusted repos
  forktrust trust --revoke /path     # remove a repo from the trust list`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTrust,
}

var (
	trustList   bool
	trustRevoke bool
)

func init() {
	trustCmd.Flags().BoolVar(&trustList, "list", false, "show all trusted repos and exit")
	trustCmd.Flags().BoolVar(&trustRevoke, "revoke", false, "remove the given path from the trust list")
}

func runTrust(_ *cobra.Command, args []string) error {
	store, err := config.LoadTrust()
	if err != nil {
		return err
	}

	if trustList {
		if len(store.Trusted) == 0 {
			fmt.Println("no trusted repos")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PATH\tSHA256")
		for _, e := range store.Trusted {
			short := e.ConfigSHA256
			if len(short) > 12 {
				short = short[:12]
			}
			fmt.Fprintf(w, "%s\t%s\n", e.Path, short)
		}
		return w.Flush()
	}

	target, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) == 1 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}

	if trustRevoke {
		if store.Revoke(abs) {
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("revoked trust for %s\n", abs)
		} else {
			fmt.Printf("not in trust list: %s\n", abs)
		}
		return nil
	}

	if err := store.Trust(abs); err != nil {
		return err
	}
	if err := store.Save(); err != nil {
		return err
	}
	sum, _ := config.SHA256RepoConfig(abs)
	short := sum
	if len(short) > 12 {
		short = short[:12]
	}
	fmt.Printf("trusted %s (sha256: %s)\n", abs, short)
	return nil
}
