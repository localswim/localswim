package v2

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var threadIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidNotificationTarget reports whether target is a canonical external chat UUID.
func ValidNotificationTarget(target string) bool {
	return threadIDPattern.MatchString(target)
}

type notifications struct {
	Enabled        bool    `json:"enabled"`
	TargetThreadID *string `json:"target_thread_id"`
}

func (value notifications) validate() error {
	if value.Enabled && value.TargetThreadID == nil {
		return invalid("notifications.target_thread_id", "required when notifications are enabled")
	}
	if value.TargetThreadID != nil && !ValidNotificationTarget(*value.TargetThreadID) {
		return invalid("notifications.target_thread_id", "must be a canonical external chat UUID")
	}
	return nil
}

func patchNotifications(current notifications, fields map[string]json.RawMessage) (notifications, error) {
	if len(fields) == 0 {
		return current, invalid("notifications", "must contain enabled or target_thread_id")
	}

	next := current
	for name, value := range fields {
		switch name {
		case "enabled":
			var enabled *bool
			if err := json.Unmarshal(value, &enabled); err != nil || enabled == nil {
				return current, invalid("notifications.enabled", "must be a boolean")
			}
			next.Enabled = *enabled
		case "target_thread_id":
			var target *string
			if err := json.Unmarshal(value, &target); err != nil {
				return current, invalid("notifications.target_thread_id", "must be an external chat UUID or null")
			}
			if target != nil {
				canonical := strings.ToLower(*target)
				next.TargetThreadID = &canonical
			} else {
				next.TargetThreadID = nil
			}
		default:
			return current, invalid("notifications", "contains an unsupported member")
		}
	}
	if err := next.validate(); err != nil {
		return current, err
	}
	return next, nil
}

const maxNotificationRouteBytes = 4096

var errNotificationRouteUnavailable = errors.New("saved notification settings unavailable")

type notificationRoute struct {
	BoardID  string `json:"board_id"`
	Revision uint64 `json:"revision"`
	notifications
}

func (route notificationRoute) entityTag() string {
	return etag("notifications-"+route.BoardID, route.Revision)
}

func (s *store) notificationRoutePath(id string) string {
	return filepath.Join(s.root, ".notifications-"+id+".json")
}

// The service owns this workspace-local file. Board capture and recovery only
// use the snapshot and its audited attachments, so they cannot move this route.
func (s *store) readNotificationRoute(id string) (notificationRoute, error) {
	path := s.notificationRoutePath(id)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return notificationRoute{BoardID: id, Revision: 1}, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxNotificationRouteBytes {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNotificationRouteBytes+1))
	if err != nil || len(data) > maxNotificationRouteBytes {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	var route notificationRoute
	if err := unmarshalCanonical(data, &route); err != nil || route.BoardID != id ||
		route.Revision == 0 || route.Revision > maxSnapshotInteger || route.notifications.validate() != nil {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	canonical, err := marshalCanonical(route)
	if err != nil || !matchesSnapshotJSON(data, canonical) ||
		checkNamedSnapshot(path, snapshotFile{data: data, info: opened}) != nil {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	return route, nil
}

func (s *store) ownerNoticeSettings(id string) (notifications, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.writable(id) != nil {
		return notifications{}, false
	}
	route, err := s.readNotificationRoute(id)
	return route.notifications, err == nil
}

// Callers hold the store lock through the authorization check and any write
// so a policy replacement or restore cannot race the route change.
func (s *store) notificationRouteForActor(id, actor string) (notificationRoute, error) {
	current, exists := s.boards[id]
	if !exists || !current.can(actor, "read") {
		return notificationRoute{}, errMissing
	}
	if !current.can(actor, "manage_notifications") {
		return notificationRoute{}, errForbidden
	}
	return s.readNotificationRoute(id)
}

func (s *store) patchNotificationRoute(id, actor, expected string, fields map[string]json.RawMessage) (notificationRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.notificationRouteForActor(id, actor)
	if err != nil {
		return notificationRoute{}, err
	}
	if err := s.writable(id); err != nil {
		return notificationRoute{}, err
	}
	if !matchesStrongTag(expected, current.entityTag()) {
		return notificationRoute{}, errStale
	}
	settings, err := patchNotifications(current.notifications, fields)
	if err != nil {
		return notificationRoute{}, err
	}
	if current.Enabled == settings.Enabled && sameText(current.TargetThreadID, settings.TargetThreadID) {
		return current, nil
	}
	if current.Revision == maxSnapshotInteger {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	next := notificationRoute{id, current.Revision + 1, settings}
	data, err := marshalCanonical(next)
	if err != nil {
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	if err := writeAtomic(s.notificationRoutePath(id), data); err != nil {
		if errors.Is(err, errWriteUncertain) {
			if s.ownerNotices != nil {
				s.ownerNotices.settingsChanged(id, false)
			}
			return notificationRoute{}, errWriteUncertain
		}
		return notificationRoute{}, errNotificationRouteUnavailable
	}
	if s.ownerNotices != nil {
		s.ownerNotices.settingsChanged(id, ownerNoticeEnabled(settings))
	}
	return next, nil
}

func (x *request) notificationResource(id string) {
	s := x.service.store
	var route notificationRoute
	var err error
	if x.r.Method == http.MethodPatch {
		expected, err := x.ifMatch()
		if err != nil {
			x.fail(err)
			return
		}
		fields, err := x.boundedBody(maxNotificationRouteBytes)
		if err != nil {
			x.fail(err)
			return
		}
		route, err = s.patchNotificationRoute(id, x.actor, expected, fields)
		if err != nil {
			x.fail(err)
			return
		}
	} else {
		s.mu.RLock()
		route, err = s.notificationRouteForActor(id, x.actor)
		s.mu.RUnlock()
		if err != nil {
			x.fail(err)
			return
		}
	}
	x.w.Header().Set("Cache-Control", "no-store")
	x.respond(http.StatusOK, route, route.entityTag())
}
