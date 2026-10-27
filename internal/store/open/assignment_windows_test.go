//go:build windows && amd64

package open

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/localswim/localswim/internal/store/schema"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Captured from the complete pre-assignment schema source (layout4), not a header substitution.
func physicalFourStatements(t *testing.T) []string {
	t.Helper()
	raw, e := os.ReadFile("testdata/layout4-statements.json")
	if e != nil {
		t.Fatal(e)
	}
	var s []string
	if e = json.Unmarshal(raw, &s); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestPhysicalLayoutFourRefusedWithoutWrites(t *testing.T) {
	p := privateDir(t)
	g, err := acquirePath(p, true)
	if err != nil {
		t.Fatal(err)
	}
	db, err := pool(g.path, false)
	if err != nil {
		g.close()
		t.Fatal(err)
	}
	c, err := db.Conn(ctx(t))
	if err != nil {
		db.Close()
		g.close()
		t.Fatal(err)
	}
	if err = configure(ctx(t), c, false, true); err != nil {
		t.Fatal(err)
	}
	execute(t, c, "BEGIN IMMEDIATE")
	for _, statement := range physicalFourStatements(t) {
		execute(t, c, statement)
	}
	execute(t, c, "INSERT INTO board(singleton,board_id,incarnation) VALUES(1,?,?)", boardID, incarnation)
	execute(t, c, "INSERT INTO policy(incarnation) VALUES(?)", incarnation)
	execute(t, c, fmt.Sprintf("PRAGMA application_id=%d; PRAGMA user_version=4", schema.ApplicationID))
	execute(t, c, "COMMIT")
	var oldCard string
	if err = c.QueryRowContext(ctx(t), "SELECT sql FROM sqlite_schema WHERE name='card'").Scan(&oldCard); err != nil || strings.Contains(oldCard, "owner_id") {
		t.Fatal("legacy fixture is not layout 4", err)
	}
	c.Close()
	db.Close()
	g.close()
	before := legacyFiles(t, p)
	if reopened, err := Existing(ctx(t), p, identity); err == nil {
		reopened.Close()
		t.Fatal("layout four accepted")
	} else if err.Error() != "unsupported storage user_version" {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, legacyFiles(t, p)) {
		t.Fatal("layout four refusal changed a file or inventory")
	}
}

func TestAssignmentSameIncarnationForeignKey(t *testing.T) {
	s, _ := newStore(t)
	withWriter(t, s, func(c *sql.Conn) {
		execute(t, c, "INSERT INTO actor(incarnation,actor_id,kind,label,origin_board,origin_incarnation) VALUES(?,?,'human','Synthetic',?,?)", incarnation, actorID, boardID, incarnation)
		execute(t, c, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)", incarnation, cardID, cardID, boardID, incarnation)
		execute(t, c, "UPDATE board SET next_ticket=2")
		execute(t, c, "INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,1,?)", incarnation, cardID)
		execute(t, c, "INSERT INTO card(incarnation,card_id,revision,title,description,owner_id) VALUES(?,?,1,'Synthetic','',?)", incarnation, cardID, actorID)
		refused(t, c, "UPDATE card SET owner_id=?", missingID)
		execute(t, c, "UPDATE card SET owner_id=NULL")
	})
}
