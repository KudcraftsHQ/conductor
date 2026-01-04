package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/hammashamzah/conductor/internal/config"
	"github.com/hammashamzah/conductor/internal/store"
	"github.com/spf13/cobra"
)

var portsCmd = &cobra.Command{
	Use:   "ports",
	Short: "Manage port allocations",
	Long:  "List and manage port allocations across all projects",
}

var portsListProject string

var portsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all allocated ports",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		portInfo := s.GetAllPortInfo()
		if len(portInfo) == 0 {
			fmt.Println("No ports allocated.")
			return nil
		}

		// Filter by project if specified
		if portsListProject != "" {
			filtered := make([]config.PortInfo, 0)
			for _, p := range portInfo {
				if p.Project == portsListProject {
					filtered = append(filtered, p)
				}
			}
			portInfo = filtered
		}

		fmt.Printf("Allocated ports: %d\n\n", len(portInfo))

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "PORT\tPROJECT\tWORKTREE\tINDEX\tLABEL")
		_, _ = fmt.Fprintln(w, "----\t-------\t--------\t-----\t-----")

		for _, p := range portInfo {
			label := p.Label
			if label == "" {
				label = "-"
			}
			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", p.Port, p.Project, p.Worktree, p.Index, label)
		}
		_ = w.Flush()

		return nil
	},
}

var portsFreeCmd = &cobra.Command{
	Use:   "free <port>",
	Short: "Manually free a port (use with caution)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := store.Load()
		if err != nil {
			return err
		}
		defer func() { _, _ = s.Close() }()

		portStr := args[0]
		portAllocations := s.GetAllPortAllocations()
		alloc, ok := portAllocations[portStr]
		if !ok {
			return fmt.Errorf("port %s is not allocated", portStr)
		}

		fmt.Printf("This port is allocated to %s/%s\n", alloc.Project, alloc.Worktree)
		fmt.Printf("Freeing this port may cause issues. Are you sure? (y/N): ")

		var response string
		_, _ = fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Cancelled.")
			return nil
		}

		// Parse the port number
		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
			return fmt.Errorf("invalid port number '%s': %w", portStr, err)
		}

		// Remove from port allocations
		s.RemovePortAllocation(port)

		// Also remove from worktree's port list
		currentPorts := s.GetWorktreePorts(alloc.Project, alloc.Worktree)
		if len(currentPorts) > 0 {
			newPorts := make([]int, 0, len(currentPorts)-1)
			for _, p := range currentPorts {
				if p != port {
					newPorts = append(newPorts, p)
				}
			}
			if err := s.SetWorktreePorts(alloc.Project, alloc.Worktree, newPorts); err != nil {
				// Worktree might not exist anymore, that's ok
				_ = err
			}
		}

		fmt.Printf("Freed port %s\n", portStr)
		return nil
	},
}

func init() {
	portsListCmd.Flags().StringVarP(&portsListProject, "project", "p", "", "Filter by project name")

	portsCmd.AddCommand(portsListCmd)
	portsCmd.AddCommand(portsFreeCmd)
}
