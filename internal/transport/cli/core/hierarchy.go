package core

import (
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"sort"
	"strings"
)

// HierarchyRead is a CLI-only presentation of one complete authorized BoardRead.
// It is deliberately absent from the HTTP transport union and route registry.
type HierarchyRead struct {
	APIVersion A.Version     `json:"apiVersion"`
	Scope      string        `json:"scope"`
	Result     HierarchyView `json:"result"`
}
type HierarchyView struct {
	Identity         A.Identity      `json:"identity"`
	Revision         A.Number        `json:"revision"`
	Root             A.CardRef       `json:"root"`
	Parent           *A.CardRef      `json:"parent"`
	DirectChildCount A.Number        `json:"directChildCount"`
	DescendantCount  A.Number        `json:"descendantCount"`
	Nodes            []HierarchyNode `json:"nodes"`
	Complete         bool            `json:"complete"`
}
type HierarchyNode struct {
	Ref      A.CardRef  `json:"ref"`
	Ticket   A.Positive `json:"ticket"`
	Title    string     `json:"title" validate:"title"`
	Lane     A.Lane     `json:"lane"`
	Priority A.Priority `json:"priority"`
	OwnerID  *A.UUID    `json:"ownerId"`
	Archived bool       `json:"archived"`
	Parent   *A.CardRef `json:"parent"`
	Depth    A.Positive `json:"depth"`
}

func hierarchyRead(board T.BoardRead, id string) (HierarchyRead, error) {
	var out HierarchyRead
	if err := T.Validate(board); err != nil {
		return out, err
	}
	b := board.Result
	cards := map[A.UUID]A.Card{}
	children := map[A.UUID][]A.Card{}
	for _, c := range b.Cards {
		cards[c.CardID] = c
		if c.Parent != nil {
			children[c.Parent.CardID] = append(children[c.Parent.CardID], c)
		}
	}
	root, ok := cards[A.UUID(id)]
	if !ok {
		return out, fmt.Errorf("root unavailable")
	}
	for key := range children {
		sort.Slice(children[key], func(i, j int) bool {
			a, b := children[key][i], children[key][j]
			return a.Ticket < b.Ticket || a.Ticket == b.Ticket && a.CardID < b.CardID
		})
	}
	ref := func(id A.UUID) A.CardRef {
		return A.CardRef{BoardID: b.Identity.BoardID, Incarnation: b.Identity.Incarnation, CardID: id}
	}
	out = HierarchyRead{APIVersion: 1, Scope: "hierarchy", Result: HierarchyView{Identity: b.Identity, Revision: b.Revision, Root: ref(root.CardID), Parent: root.Parent, DirectChildCount: A.Number(len(children[root.CardID])), Nodes: []HierarchyNode{}, Complete: true}}
	var visit func(A.UUID, A.Positive)
	visit = func(id A.UUID, depth A.Positive) {
		for _, c := range children[id] {
			out.Result.Nodes = append(out.Result.Nodes, HierarchyNode{Ref: ref(c.CardID), Ticket: c.Ticket, Title: c.Title, Lane: c.Lane, Priority: c.Priority, OwnerID: c.OwnerID, Archived: c.Archived, Parent: c.Parent, Depth: depth})
			visit(c.CardID, depth+1)
		}
	}
	visit(root.CardID, 1)
	out.Result.DescendantCount = A.Number(len(out.Result.Nodes))
	return out, nil
}
func readableHierarchy(h HierarchyRead, page int) string {
	b := h.Result
	var out strings.Builder
	pages := (len(b.Nodes) + 19) / 20
	if pages == 0 {
		pages = 1
	}
	start := (page - 1) * 20
	end := start + 20
	if end > len(b.Nodes) {
		end = len(b.Nodes)
	}
	first := start + 1
	if end == 0 {
		first = 0
	}
	fmt.Fprintf(&out, "Hierarchy %s/%s/%s; board revision %d\nDirect children: %d; descendants: %d\nRows %d-%d of %d; page %d of %d\n", b.Root.BoardID, b.Root.Incarnation, b.Root.CardID, b.Revision, b.DirectChildCount, b.DescendantCount, first, end, len(b.Nodes), page, pages)
	if b.Parent == nil {
		out.WriteString("Parent: No parent\n")
	} else {
		fmt.Fprintf(&out, "Parent: %s/%s/%s\n", b.Parent.BoardID, b.Parent.Incarnation, b.Parent.CardID)
	}
	for _, n := range b.Nodes[start:end] {
		owner := "Unassigned"
		if n.OwnerID != nil {
			owner = string(*n.OwnerID)
		}
		fmt.Fprintf(&out, "Depth %d #%d %s (%s; %s; owner: %s; archived: %t) %s; parent %s\n", n.Depth, n.Ticket, advisoryText(n.Title), n.Lane, n.Priority, owner, n.Archived, n.Ref.CardID, n.Parent.CardID)
	}
	if page == pages {
		out.WriteString("End of hierarchy.\n")
	} else {
		fmt.Fprintf(&out, "Next local page: %d (a new invocation reads a fresh board).\n", page+1)
	}
	return out.String()
}
