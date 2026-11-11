package v2

import (
	"cmp"
	"maps"
	"math/big"
	"math/bits"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type cardFilters struct {
	ticket         uint64
	laneIDs        map[string]bool
	unfinishedOnly bool
	archived       string
	sort           string
	query          string
	searchTicket   string
	searchContent  bool
}

func readCardFilters(r *http.Request) (cardFilters, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return cardFilters{}, invalid("ticket", "Malformed query string.")
	}
	result := cardFilters{archived: "exclude", sort: "ticket"}
	if entries := query["lane_id"]; len(entries) != 0 {
		result.laneIDs = make(map[string]bool, len(entries))
		for _, raw := range entries {
			value, valid := normalizeID(raw)
			if !valid {
				return cardFilters{}, invalid("lane_id", "Expected a UUIDv4.")
			}
			result.laneIDs[value] = true
		}
	}
	for _, field := range []string{"ticket", "archived", "sort", "q", "search_content", "unfinished_only"} {
		entries := query[field]
		if len(entries) == 0 {
			continue
		}
		if len(entries) > 1 {
			return cardFilters{}, invalid(field, "Repeated query parameter.")
		}
		raw := entries[0]
		if raw == "" {
			return cardFilters{}, invalid(field, "Expected a nonempty value.")
		}
		switch field {
		case "ticket":
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil || value == 0 || value > maxSnapshotInteger {
				return cardFilters{}, invalid(field, "Expected a positive safe integer.")
			}
			result.ticket = value
		case "archived":
			value := asciiLowerEnum(raw)
			if !slices.Contains([]string{"exclude", "include", "only"}, value) {
				return cardFilters{}, invalid(field, "Expected exclude, include, or only.")
			}
			result.archived = value
		case "sort":
			value := asciiLowerEnum(raw)
			if !slices.Contains([]string{"ticket", "lane", "triage"}, value) {
				return cardFilters{}, invalid(field, "Expected ticket, lane, or triage.")
			}
			result.sort = value
		case "q":
			trimmed := strings.TrimFunc(raw, func(char rune) bool {
				return unicode.IsSpace(char) || char >= 0x1c && char <= 0x1f
			})
			if !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > 1024 || trimmed == "" {
				return cardFilters{}, invalid(field, "Expected 1 to 1024 characters with non-whitespace text.")
			}
			result.query = foldSearchText(trimmed)
			ticketText := strings.TrimPrefix(result.query, "#")
			if unsupportedSearchTicket(ticketText) {
				return cardFilters{}, invalid(field, "Ticket search requires a supported decimal number.")
			}
			result.searchTicket = decimalSearchTicket(ticketText)
		case "search_content":
			if raw != "true" && raw != "false" {
				return cardFilters{}, invalid(field, "Expected true or false.")
			}
			result.searchContent = raw == "true"
		case "unfinished_only":
			if raw != "true" && raw != "false" {
				return cardFilters{}, invalid(field, "Expected true or false.")
			}
			result.unfinishedOnly = raw == "true"
		}
	}
	return result, nil
}

func (filters cardFilters) laneScope() string {
	return strings.Join(slices.Sorted(maps.Keys(filters.laneIDs)), ",")
}

func asciiLowerEnum(raw string) string {
	if strings.IndexFunc(raw, func(char rune) bool { return char >= utf8.RuneSelf }) >= 0 {
		return ""
	}
	return strings.ToLower(raw)
}

func selectCards(s snapshot, filters cardFilters) []card {
	// Derive from every card before applying visibility, search, or pagination.
	cards := cardsWithTriage(s)
	ranks := make(map[string]int, len(s.Priorities))
	for _, priority := range s.Priorities {
		ranks[priority.ID] = priority.Rank
	}
	if filters.sort == "lane" {
		view := s
		view.Cards = cards
		// archived=include/only explicitly makes archived cards eligible for this view.
		if filters.archived != "exclude" {
			view.Cards = slices.Clone(cards)
			for i := range view.Cards {
				view.Cards[i].Archived = false
			}
		}
		lanes := slices.Clone(s.Lanes)
		slices.SortFunc(lanes, func(a, b lane) int { return cmp.Compare(a.Position, b.Position) })
		cards = make([]card, 0, len(cards))
		archived := make(map[string]bool)
		for _, item := range s.Cards {
			archived[item.ID] = item.Archived
		}
		for _, lane := range lanes {
			for _, item := range orderedLaneCards(view, lane.ID) {
				item.Archived = archived[item.ID]
				cards = append(cards, item)
			}
		}
	} else {
		slices.SortFunc(cards, func(a, b card) int {
			if filters.sort == "triage" {
				if order := cmp.Compare(a.TriageScore, b.TriageScore); order != 0 {
					return order
				}
				if order := cmp.Compare(ranks[a.PriorityID], ranks[b.PriorityID]); order != 0 {
					return order
				}
			}
			return cmp.Compare(a.Ticket, b.Ticket)
		})
	}
	contentMatches := make(map[string]bool)
	if filters.searchContent && filters.query != "" && filters.searchTicket == "" {
		for _, comment := range s.Comments {
			if strings.Contains(foldSearchText(comment.Text), filters.query) {
				contentMatches[comment.CardID] = true
			}
		}
	}
	rows := make([]card, 0, len(cards))
	for _, item := range cards {
		if filters.ticket != 0 && item.Ticket != filters.ticket ||
			len(filters.laneIDs) != 0 && !filters.laneIDs[item.LaneID] ||
			filters.unfinishedOnly && item.CompletedAt != nil ||
			filters.archived != "include" && item.Archived != (filters.archived == "only") {
			continue
		}
		matches := filters.query == "" || strings.Contains(foldSearchText(item.Title), filters.query) ||
			strings.Contains(item.ID, filters.query) ||
			filters.searchContent && (strings.Contains(foldSearchText(item.Description), filters.query) || contentMatches[item.ID])
		if filters.searchTicket != "" {
			matches = strconv.FormatUint(item.Ticket, 10) == filters.searchTicket
		}
		if matches {
			rows = append(rows, item)
		}
	}
	return rows
}

func cardsWithTriage(s snapshot) []card {
	cards := slices.Clone(s.Cards)
	indexes := make(map[string]int, len(cards))
	for i, item := range cards {
		indexes[item.ID] = i
	}
	full := make([][]int, len(cards))
	unfinished := make([][]int, len(cards))
	for _, link := range s.Relationships {
		if link.Kind == "blocks" {
			source, target := indexes[link.SourceCardID], indexes[link.TargetCardID]
			full[source] = append(full[source], target)
			if cards[source].CompletedAt == nil && cards[target].CompletedAt == nil {
				unfinished[source] = append(unfinished[source], target)
			}
		}
	}
	_, depths := blockingMetrics(full, false)
	counts, _ := blockingMetrics(unfinished, true)
	floor := calculatePriorityFloor(&s, nil)
	for i := range cards {
		cards[i].BlockingDepth = depths[i]
		cards[i].BlockedCardCount = counts[i]
		cards[i].TriageScore = (floor.ranks[floor.ids[i]]+1)*100 - counts[i]
	}
	return cards
}

// Condense cycles and accumulate descendant bitsets once per component. Distinct
// reachability counts diamonds once and excludes the starting card within cycles.
// Depth retains the full historical graph, including completed cards.
func blockingMetrics(edges [][]int, includeCounts bool) ([]int, []int) {
	components, membership := laneComponents(edges)
	parents := make([][]int, len(components))
	remaining := make([]int, len(components))
	depths := make([]int, len(components))
	for group, members := range components {
		var children map[int]bool
		for _, member := range members {
			for _, target := range edges[member] {
				child := membership[target]
				if child != group && !children[child] {
					if children == nil {
						children = make(map[int]bool)
					}
					children[child] = true
					parents[child] = append(parents[child], group)
					remaining[group]++
				}
			}
		}
	}
	var reachable []big.Int
	var bitStart []int
	if includeCounts {
		reachable = make([]big.Int, len(components))
		bitStart = make([]int, len(components))
		nextBit := 0
		for group, members := range components {
			// Only components reachable from another component occupy bit
			// positions. Count each component's own peers directly.
			if len(parents[group]) > 0 {
				bitStart[group] = nextBit
				nextBit += len(members)
			}
		}
	}
	ready := make([]int, 0, len(components))
	for group, count := range remaining {
		if count == 0 {
			ready = append(ready, group)
		}
	}
	counts, cardDepths := make([]int, len(edges)), make([]int, len(edges))
	for len(ready) > 0 {
		last := len(ready) - 1
		child := ready[last]
		ready = ready[:last]
		count := 0
		if includeCounts {
			count = len(components[child]) - 1
			for _, word := range reachable[child].Bits() {
				count += bits.OnesCount(uint(word))
			}
			if len(parents[child]) > 0 {
				for i := range components[child] {
					reachable[child].SetBit(&reachable[child], bitStart[child]+i, 1)
				}
			}
		}
		for _, member := range components[child] {
			counts[member], cardDepths[member] = count, depths[child]
		}
		for _, parent := range parents[child] {
			if includeCounts {
				reachable[parent].Or(&reachable[parent], &reachable[child])
			}
			depths[parent] = max(depths[parent], depths[child]+1)
			remaining[parent]--
			if remaining[parent] == 0 {
				ready = append(ready, parent)
			}
		}
		if includeCounts {
			reachable[child] = big.Int{}
		}
	}
	return counts, cardDepths
}

// Unicode decimal digit blocks contain successive 0..9 ranges, including the
// contiguous mathematical digit styles. Match Python's supported decimal tickets.
func decimalSearchTicket(text string) string {
	if text == "" {
		return ""
	}
	var digits strings.Builder
	for _, char := range text {
		if !unicode.IsDigit(char) {
			return ""
		}
		value := 0
		if char <= 0xffff {
			for _, block := range unicode.Digit.R16 {
				if uint32(char) >= uint32(block.Lo) && uint32(char) <= uint32(block.Hi) {
					value = int((uint32(char)-uint32(block.Lo))/uint32(block.Stride)) % 10
					break
				}
			}
		} else {
			for _, block := range unicode.Digit.R32 {
				if uint32(char) >= block.Lo && uint32(char) <= block.Hi {
					value = int((uint32(char)-block.Lo)/block.Stride) % 10
					break
				}
			}
		}
		digits.WriteByte(byte('0' + value))
	}
	canonical := strings.TrimLeft(digits.String(), "0")
	if canonical == "" {
		return "0"
	}
	return canonical
}

func unsupportedSearchTicket(text string) bool {
	unsupported := false
	for _, char := range text {
		if unicode.IsDigit(char) {
			continue
		}
		if !slices.ContainsFunc(searchNonDecimalDigits[:], func(bounds [2]rune) bool {
			return char >= bounds[0] && char <= bounds[1]
		}) {
			return false
		}
		unsupported = true
	}
	return unsupported
}
