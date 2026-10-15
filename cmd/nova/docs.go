package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AutumnsGrove/nova/docs"
)

// docsCommand lists, prints, and searches the embedded documentation.
func docsCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "docs [topic|search]",
		Short: "Print embedded documentation",
		Long:  "Print an embedded document. With no topic, list the topics; with `search <terms>`, rank topics by term frequency.",
		RunE: func(_ *cobra.Command, args []string) error {
			topics := docs.Topics()
			switch {
			case len(args) == 0:
				for _, t := range topics {
					fmt.Fprintln(stdout, t.Name)
				}
				return nil
			case args[0] == "search":
				results := docs.Search(topics, args[1:])
				if len(results) == 0 {
					fmt.Fprintln(stdout, "no matching topics")
					return nil
				}
				for _, r := range results {
					fmt.Fprintf(stdout, "%s\t%s\n", r.Name, r.Excerpt)
				}
				return nil
			}
			name := args[0]
			for _, t := range topics {
				if t.Name == name {
					fmt.Fprint(stdout, t.Body)
					if !strings.HasSuffix(t.Body, "\n") {
						fmt.Fprintln(stdout)
					}
					return nil
				}
			}
			fmt.Fprintf(stdout, "unknown topic %q; topics: %s\n", name, strings.Join(topicNames(topics), ", "))
			return nil
		},
	}
}

// topicNames returns the topic names in order.
func topicNames(topics []docs.Topic) []string {
	out := make([]string, len(topics))
	for i, t := range topics {
		out[i] = t.Name
	}
	return out
}
