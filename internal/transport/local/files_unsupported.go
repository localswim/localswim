//go:build !windows || !amd64

package local

func safeDir(string) bool                  { return false }
func pinRoot(string, bool) (func(), error) { return nil, errLocal }
func privateDir(string) error              { return errLocal }
func lockRoot(string) (func(), error)      { return nil, errLocal }
func readPrivate(string) ([]byte, error)   { return nil, errLocal }
func publish(string, []byte, bool) error   { return errLocal }

func lockExistingRoot(string) (func(), error)          { return nil, errLocal }
func readPrivateBounded(string, int64) ([]byte, error) { return nil, errLocal }
