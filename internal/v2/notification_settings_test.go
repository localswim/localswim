package v2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNotificationSettingsPatch(t *testing.T) {
	target := "abcdef01-2345-7678-abcd-ef0123456789"
	configured := notifications{TargetThreadID: &target}
	enabled := notifications{Enabled: true, TargetThreadID: &target}
	tests := []struct {
		name    string
		current notifications
		patch   string
		want    notifications
	}{
		{"target stays off and normalizes", notifications{}, `{"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, configured},
		{"explicit enable retains target", configured, `{"enabled":true}`, enabled},
		{"target preserves enabled", enabled, `{"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, enabled},
		{"explicit disable retains target", enabled, `{"enabled":false}`, configured},
		{"clear while enabled", enabled, `{"target_thread_id":null}`, notifications{Enabled: true}},
		{"enable without a target", notifications{}, `{"enabled":true}`, notifications{Enabled: true}},
		{"clear while disabled", configured, `{"target_thread_id":null}`, notifications{}},
		{"disable and clear atomically", enabled, `{"enabled":false,"target_thread_id":null}`, notifications{}},
		{"enable and target atomically", notifications{}, `{"enabled":true,"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, enabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := patchNotifications(test.current, json.RawMessage(test.patch))
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("patch %s produced %+v, %v, want %+v", test.patch, got, err, test.want)
			}
		})
	}
}

func TestNotificationSettingsRejectsInvalidPatch(t *testing.T) {
	target := "abcdef01-2345-8678-bbcd-ef0123456789"
	current := notifications{Enabled: true, TargetThreadID: &target}
	for _, patch := range []string{
		`null`, `[]`, `{}`, `{"Enabled":true}`, `{"enabled":true,"enabled":false}`,
		`{"enabled":null}`, `{"enabled":0}`, `{"enabled":"false"}`,
		`{"enabled":false,"actor_id":"ignored"}`, `{"target_thread_id":true}`,
		`{"target_thread_id":"abcdef01-2345-9678-abcd-ef0123456789"}`,
		`{"target_thread_id":"abcdef01-2345-8678-cbcd-ef0123456789"}`,
	} {
		got, err := patchNotifications(current, json.RawMessage(patch))
		if err == nil || !reflect.DeepEqual(got, current) {
			t.Errorf("patch %s produced %+v, %v, want unchanged settings and error", patch, got, err)
		}
	}
}
