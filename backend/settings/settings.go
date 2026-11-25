// Package settings holds the operator's settings the owner may change from
// the portal. A value saved there wins over the environment, which wins over
// the code's default. Each setting is registered by the package that reads
// it, with the check it already makes, so its rule lives in one place.
//
// The saved values are a ConfigMap in the system namespace, read again once
// a reading is a few seconds old, so every backend follows a change.
package settings

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"kubendt/kubeclient"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// ConfigMapName holds the saved values, one key per setting.
const ConfigMapName = "kubendt-internal-settings"

// The groups, in the order the page shows them.
const (
	GroupPasswords = "passwords"
	GroupQuotas    = "quotas"
	GroupResources = "resources"
	GroupLabs      = "labs"
)

var groupOrder = map[string]int{GroupPasswords: 0, GroupQuotas: 1, GroupResources: 2, GroupLabs: 3}

// Setting is one value the owner may change, named by its environment
// variable. Check answers whether a value is usable, the empty one included.
type Setting struct {
	Key     string
	Group   string
	Label   string
	Hint    string
	Unit    string
	Default string
	Options []string
	Check   func(string) error
	// Order places it within its group, registration order breaking ties.
	Order int
}

var (
	regMu    sync.RWMutex
	registry = map[string]Setting{}
	order    []string
)

// Register adds a setting, from the init of the package that reads it.
func Register(s Setting) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[s.Key]; !dup {
		order = append(order, s.Key)
	}
	registry[s.Key] = s
}

func lookup(key string) (Setting, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	s, ok := registry[key]
	return s, ok
}

var configMaps = func() corev1client.ConfigMapInterface {
	if kubeclient.Clientset == nil {
		return nil
	}
	return kubeclient.Clientset.CoreV1().ConfigMaps(kubeclient.SystemNamespace())
}

// How long a reading of the ConfigMap stands. The session check asks on
// every request, so this is what keeps the API server out of the hot path.
const freshFor = 5 * time.Second

var (
	mu       sync.Mutex
	snapshot map[string]string
	readAt   time.Time
	warned   bool
)

// saved is the ConfigMap's values as last read. Without a cluster there are
// none; on an error the last reading stands.
func saved() map[string]string {
	mu.Lock()
	defer mu.Unlock()
	if snapshot != nil && time.Since(readAt) < freshFor {
		return snapshot
	}
	client := configMaps()
	if client == nil {
		return map[string]string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, err := client.Get(ctx, ConfigMapName, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		snapshot, warned = map[string]string{}, false
	case err != nil:
		if !warned {
			log.Printf("⚠️ Could not read the settings, keeping the last reading: %v", err)
			warned = true
		}
		if snapshot == nil {
			snapshot = map[string]string{}
		}
	default:
		snapshot, warned = copyOf(cm.Data), false
	}
	readAt = time.Now()
	return snapshot
}

func copyOf(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Value is the setting's value: the saved one, else the environment's, else
// empty, which each reader takes as its default.
func Value(key string) string {
	if v, ok := saved()[key]; ok {
		return v
	}
	return os.Getenv(key)
}

// Named is how an error names the setting: where its value came from.
func Named(key string) string {
	if _, ok := saved()[key]; ok {
		return key + " (saved in the settings)"
	}
	return key
}

// Entry is a setting as the page shows it.
type Entry struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Hint    string   `json:"hint,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Default string   `json:"default"`
	Options []string `json:"options,omitempty"`
	// Env is the environment's value, Saved the portal's when there is one.
	Env    string  `json:"env"`
	Saved  *string `json:"saved"`
	Value  string  `json:"value"`
	Source string  `json:"source" enums:"saved,environment,default"`
}

// List is every setting, by group, with where its value comes from.
func List() []Entry {
	regMu.RLock()
	keys := append([]string(nil), order...)
	regMu.RUnlock()
	values := saved()
	out := make([]Entry, 0, len(keys))
	for _, key := range keys {
		s, _ := lookup(key)
		e := Entry{Key: key, Group: s.Group, Label: s.Label, Hint: s.Hint, Unit: s.Unit,
			Default: s.Default, Options: s.Options, Env: os.Getenv(key)}
		if v, ok := values[key]; ok {
			v := v
			e.Saved, e.Value, e.Source = &v, v, "saved"
		} else if e.Env != "" {
			e.Value, e.Source = e.Env, "environment"
		} else {
			e.Value, e.Source = s.Default, "default"
		}
		out = append(out, e)
	}
	rank := func(e Entry) (int, int) {
		s, _ := lookup(e.Key)
		return groupOrder[e.Group], s.Order
	}
	sort.SliceStable(out, func(i, j int) bool {
		gi, oi := rank(out[i])
		gj, oj := rank(out[j])
		return gi < gj || (gi == gj && oi < oj)
	})
	return out
}

// Error is a value refused, with the setting it was for.
type Error struct {
	Key    string
	Reason string
}

func (e *Error) Error() string {
	if strings.Contains(e.Reason, e.Key) {
		return e.Reason
	}
	return e.Key + ": " + e.Reason
}

// Save applies changes: a value is saved, nil drops the key so the
// environment rules again. Every value is checked before anything is written.
func Save(changes map[string]*string) error {
	for key, v := range changes {
		s, ok := lookup(key)
		if !ok {
			return &Error{Key: key, Reason: "not a setting the portal may change"}
		}
		if v != nil && s.Check != nil {
			if err := s.Check(strings.TrimSpace(*v)); err != nil {
				return &Error{Key: key, Reason: err.Error()}
			}
		}
	}
	client := configMaps()
	if client == nil {
		return fmt.Errorf("no cluster to keep the settings in yet")
	}
	ctx := context.Background()
	for attempt := 0; attempt < 5; attempt++ {
		cm, err := client.Get(ctx, ConfigMapName, metav1.GetOptions{})
		create := apierrors.IsNotFound(err)
		if err != nil && !create {
			return fmt.Errorf("could not read the settings: %w", err)
		}
		if create {
			cm = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ConfigMapName, Labels: map[string]string{
				"app.kubernetes.io/managed-by": "kubendt-backend",
				"kubendt/internal":             "true",
				"kubendt/kind":                 "settings",
			}}}
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		for key, v := range changes {
			if v == nil {
				delete(cm.Data, key)
			} else {
				cm.Data[key] = strings.TrimSpace(*v)
			}
		}
		if create {
			_, err = client.Create(ctx, cm, metav1.CreateOptions{})
		} else {
			_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
		}
		if err == nil {
			mu.Lock()
			snapshot, readAt = copyOf(cm.Data), time.Now()
			mu.Unlock()
			return nil
		}
		if !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not save the settings: %w", err)
		}
	}
	return fmt.Errorf("the settings kept changing while they were saved")
}

// forget drops the reading, for the tests.
func forget() {
	mu.Lock()
	snapshot, readAt = nil, time.Time{}
	mu.Unlock()
}
