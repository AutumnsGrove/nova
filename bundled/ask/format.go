package ask

import (
	"fmt"
	"slices"
	"strings"

	"github.com/AutumnsGrove/nova/proto"
)

// formatAnswers renders one block per question, in question order. Notes are
// included verbatim on their own line.
func formatAnswers(questions []question, resp proto.UIResponse) string {
	answers := map[string]proto.UIAnswer{}
	for _, a := range resp.Answers {
		answers[a.ID] = a
	}
	var out []string
	for i, q := range questions {
		a := answers[q.ID]
		var b strings.Builder
		fmt.Fprintf(&b, "Q%d [%s] %s\n", i+1, q.ID, q.Prompt)
		b.WriteString("Answer: ")
		switch {
		case a.Skipped || (a.Value == "" && len(a.Values) == 0):
			b.WriteString("skipped")
		case q.Kind == kindText:
			b.WriteString(a.Value)
		default:
			// The form answers with option ids, or with free text when the user
			// picked Other; a client may send both.
			parts := make([]string, 0, len(a.Values)+1)
			for _, id := range a.Values {
				parts = append(parts, optionLabel(q, id))
			}
			if a.Value != "" {
				parts = append(parts, "Other: "+a.Value)
			}
			b.WriteString(strings.Join(parts, ", "))
		}
		if a.Notes != "" {
			b.WriteString("\nNote: " + a.Notes)
		}
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}

// optionLabel maps an option id to its label, falling back to the id.
func optionLabel(q question, id string) string {
	if i := slices.IndexFunc(q.Options, func(o option) bool { return o.ID == id }); i >= 0 {
		return q.Options[i].Label
	}
	return id
}
