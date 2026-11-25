package settings

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

const testKey = "KUBENDT_TEST_SETTING"

func fakeConfigMaps(t *testing.T) corev1client.ConfigMapInterface {
	t.Helper()
	cs := fake.NewClientset()
	prev := configMaps
	configMaps = func() corev1client.ConfigMapInterface { return cs.CoreV1().ConfigMaps("kubendt-system") }
	forget()
	t.Cleanup(func() {
		configMaps = prev
		forget()
	})
	return cs.CoreV1().ConfigMaps("kubendt-system")
}

func init() {
	Register(Setting{Key: testKey, Group: GroupLabs, Label: "A test", Default: "7", Check: func(v string) error {
		if v == "" {
			return nil
		}
		if n, err := strconv.Atoi(v); err != nil || n < 1 {
			return fmt.Errorf("a number from 1")
		}
		return nil
	}})
}

func entry(key string) Entry {
	for _, e := range List() {
		if e.Key == key {
			return e
		}
	}
	return Entry{}
}

func TestSavedBeatsEnvironmentBeatsDefault(t *testing.T) {
	cms := fakeConfigMaps(t)
	if e := entry(testKey); e.Value != "7" || e.Source != "default" {
		t.Fatalf("nothing set is the default: %+v", e)
	}
	t.Setenv(testKey, "3")
	if Value(testKey) != "3" || entry(testKey).Source != "environment" {
		t.Fatalf("the environment rules when nothing is saved: %q %+v", Value(testKey), entry(testKey))
	}
	five := "5"
	if err := Save(map[string]*string{testKey: &five}); err != nil {
		t.Fatal(err)
	}
	if Value(testKey) != "5" || Named(testKey) != testKey+" (saved in the settings)" {
		t.Fatalf("a saved value wins: %q %q", Value(testKey), Named(testKey))
	}
	cm, err := cms.Get(context.Background(), ConfigMapName, metav1.GetOptions{})
	if err != nil || cm.Data[testKey] != "5" || cm.Labels["kubendt/kind"] != "settings" {
		t.Fatalf("kept in the ConfigMap: %+v %v", cm, err)
	}
	if err := Save(map[string]*string{testKey: nil}); err != nil {
		t.Fatal(err)
	}
	if Value(testKey) != "3" || entry(testKey).Saved != nil {
		t.Fatalf("dropping the saved value gives the environment back: %q", Value(testKey))
	}
}

func TestSaveChecksEverythingFirst(t *testing.T) {
	cms := fakeConfigMaps(t)
	good, bad := "4", "0"
	err := Save(map[string]*string{testKey: &bad})
	var refused *Error
	if !errors.As(err, &refused) || refused.Key != testKey {
		t.Fatalf("a value its check refuses is an error naming the setting: %v", err)
	}
	if err := Save(map[string]*string{"KUBENDT_ADMIN_PASSWORD": &good}); !errors.As(err, &refused) {
		t.Fatalf("a variable that is not a setting is refused: %v", err)
	}
	if _, err := cms.Get(context.Background(), ConfigMapName, metav1.GetOptions{}); err == nil {
		t.Fatal("nothing is written when a value is refused")
	}
}
