package cli

import "github.com/spf13/cobra"

func addRootCommandGroups(root *cobra.Command) {
	addCommandGroup(root, "discovery", "Find and explore:",
		NewFindCmd(), NewSearchCmd(), NewOpenCmd(), NewBacklinksCmd(), NewLinksCmd(),
		NewRelatedCmd(), NewContextCmd(), NewGraphCmd(), NewTagsCmd(), NewValuesCmd(), NewViewsCmd(), NewVaultCmd())
	addCommandGroup(root, "editing", "Capture and edit:",
		NewCaptureCmd(), NewNewCmd(), NewMoveCmd(), NewRenameCmd(), NewRemoveCmd(),
		NewSetCmd(), NewUnsetCmd(), NewPinCmd(), NewUnpinCmd(), NewReplaceCmd(),
		NewRelinkCmd(), NewRenameKeyCmd())
	addCommandGroup(root, "maintenance", "Maintain and recover:",
		NewBackupCmd(), NewCheckpointCmd(), NewDoctorCmd(), NewHistoryCmd(), NewIndexCmd(),
		NewLintCmd(), NewRepairCmd(), NewRestoreCmd(), NewSchemaCmd(), NewUndoCmd())
	addCommandGroup(root, "setup", "Setup and browser:", NewInitCmd(), NewConfigCmd(), NewServeCmd())
	root.AddGroup(&cobra.Group{ID: "help", Title: "Help and shell integration:"})
	root.SetHelpCommandGroupID("help")
	root.SetCompletionCommandGroupID("help")
}

func addCommandGroup(root *cobra.Command, id, title string, commands ...*cobra.Command) {
	root.AddGroup(&cobra.Group{ID: id, Title: title})
	for _, command := range commands {
		command.GroupID = id
	}
	root.AddCommand(commands...)
}
