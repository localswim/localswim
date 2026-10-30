// Package hierarchy validates the complete current bounded forest.
package hierarchy

import "errors"

const MaxDepth = 64
const MaxFamily = 1000

// Validate takes every full card as a key; an empty parent denotes a root.
// Every call validates the resulting complete snapshot, including subtrees.
func Validate(parents map[string]string) error {
	families := map[string]int{}
	for id := range parents {
		seen := map[string]bool{}
		at := id
		depth := 0
		for {
			if seen[at] {
				return errors.New("invalid hierarchy")
			}
			seen[at] = true
			parent, ok := parents[at]
			if !ok {
				return errors.New("invalid hierarchy")
			}
			if parent == "" {
				families[at]++
				if families[at] > MaxFamily {
					return errors.New("invalid hierarchy")
				}
				break
			}
			depth++
			if depth > MaxDepth {
				return errors.New("invalid hierarchy")
			}
			at = parent
		}
	}
	return nil
}
