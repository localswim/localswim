package v2

import (
	"cmp"
	"container/heap"
	"slices"
)

type boardLanesView struct {
	Board board  `json:"board"`
	Lanes []lane `json:"lanes"`
	Cards []card `json:"cards"`
}

func (x *request) boardLanes(s snapshot) {
	x.respond(200, visibleBoardLanes(s), s.Board.ETag)
}

func visibleBoardLanes(s snapshot) boardLanesView {
	lanes := slices.Clone(s.Lanes)
	slices.SortFunc(lanes, func(a, b lane) int { return cmp.Compare(a.Position, b.Position) })
	view := boardLanesView{Board: s.Board, Lanes: make([]lane, 0, len(lanes)), Cards: make([]card, 0, len(s.Cards))}
	for _, item := range lanes {
		if !item.Retired {
			view.Lanes = append(view.Lanes, item)
			view.Cards = append(view.Cards, orderedLaneCards(s, item.ID)...)
		}
	}
	return view
}

// Only direct blocking links between visible cards of the same rank and lane
// constrain this view. Collapsing strongly connected components makes every
// cycle contiguous without dropping its outgoing ordering constraints.
func orderedLaneCards(s snapshot, laneID string) []card {
	cards := make([]card, 0)
	indexes := make(map[string]int)
	ranks := make(map[string]int, len(s.Priorities))
	for _, priority := range s.Priorities {
		ranks[priority.ID] = priority.Rank
	}
	for _, item := range s.Cards {
		if item.LaneID == laneID && !item.Archived {
			indexes[item.ID] = len(cards)
			cards = append(cards, item)
		}
	}
	edges := make([][]int, len(cards))
	for _, link := range s.Relationships {
		source, sourceOK := indexes[link.SourceCardID]
		target, targetOK := indexes[link.TargetCardID]
		if link.Kind == "blocks" && sourceOK && targetOK && ranks[cards[source].PriorityID] == ranks[cards[target].PriorityID] {
			edges[source] = append(edges[source], target)
		}
	}
	components, membership := laneComponents(edges)
	groups := make([]laneOrderGroup, len(components))
	for i, members := range components {
		group := &groups[i]
		group.cards = make([]card, 0, len(members))
		for _, member := range members {
			group.cards = append(group.cards, cards[member])
		}
		slices.SortFunc(group.cards, func(a, b card) int { return cmp.Compare(a.Ticket, b.Ticket) })
		group.rank = ranks[group.cards[0].PriorityID]
	}
	seen := make(map[[2]int]bool)
	for source, targets := range edges {
		for _, target := range targets {
			from, to := membership[source], membership[target]
			key := [2]int{from, to}
			if from != to && !seen[key] {
				seen[key] = true
				groups[from].next = append(groups[from].next, to)
				groups[to].incoming++
			}
		}
	}
	ready := &laneOrderHeap{groups: groups}
	for i, group := range groups {
		if group.incoming == 0 {
			heap.Push(ready, i)
		}
	}
	ordered := make([]card, 0, len(cards))
	for ready.Len() > 0 {
		index := heap.Pop(ready).(int)
		ordered = append(ordered, groups[index].cards...)
		for _, target := range groups[index].next {
			groups[target].incoming--
			if groups[target].incoming == 0 {
				heap.Push(ready, target)
			}
		}
	}
	return ordered
}

func laneComponents(edges [][]int) ([][]int, []int) {
	indices := make([]int, len(edges))
	low := make([]int, len(edges))
	onStack := make([]bool, len(edges))
	membership := make([]int, len(edges))
	stack := make([]int, 0, len(edges))
	components := make([][]int, 0)
	sequence := 0
	var visit func(int)
	visit = func(node int) {
		sequence++
		indices[node], low[node] = sequence, sequence
		stack = append(stack, node)
		onStack[node] = true
		for _, target := range edges[node] {
			if indices[target] == 0 {
				visit(target)
				low[node] = min(low[node], low[target])
			} else if onStack[target] {
				low[node] = min(low[node], indices[target])
			}
		}
		if low[node] != indices[node] {
			return
		}
		members := make([]int, 0)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			membership[member] = len(components)
			members = append(members, member)
			if member == node {
				break
			}
		}
		components = append(components, members)
	}
	for node := range edges {
		if indices[node] == 0 {
			visit(node)
		}
	}
	return components, membership
}

type laneOrderGroup struct {
	cards    []card
	rank     int
	next     []int
	incoming int
}

type laneOrderHeap struct {
	groups []laneOrderGroup
	items  []int
}

func (h laneOrderHeap) Len() int { return len(h.items) }
func (h laneOrderHeap) Less(i, j int) bool {
	a, b := h.groups[h.items[i]], h.groups[h.items[j]]
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	return a.cards[0].Ticket < b.cards[0].Ticket
}
func (h laneOrderHeap) Swap(i, j int)  { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *laneOrderHeap) Push(item any) { h.items = append(h.items, item.(int)) }
func (h *laneOrderHeap) Pop() any {
	last := len(h.items) - 1
	item := h.items[last]
	h.items = h.items[:last]
	return item
}
