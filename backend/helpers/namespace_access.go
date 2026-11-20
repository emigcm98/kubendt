package helpers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"kubendt/auth"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// Who may do what in a namespace lives on the Namespace object itself, as
// annotations, so it dies with the namespace, shows in kubectl and reaches
// every backend: kubendt/owner names the account the lab belongs to, and
// kubendt/access lists its guests as name=level pairs. A namespace with no
// owner, from before the accounts, is the admins' until one gives it away.
const (
	OwnerAnnotation  = "kubendt/owner"
	AccessAnnotation = "kubendt/access"
	accessCacheFor   = 5 * time.Second
)

// NamespaceAccess is a namespace's owner and guests.
type NamespaceAccess struct {
	Owner  string            `json:"owner"`
	Guests map[string]string `json:"guests"`
}

// ParseNamespaceAccess reads the owner and guests off a Namespace object.
func ParseNamespaceAccess(ns metav1.Object) NamespaceAccess {
	a := NamespaceAccess{Owner: ns.GetAnnotations()[OwnerAnnotation], Guests: map[string]string{}}
	for _, pair := range strings.Split(ns.GetAnnotations()[AccessAnnotation], ",") {
		name, level, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && name != "" && auth.ValidLevel(level) {
			a.Guests[name] = level
		}
	}
	return a
}

func formatGuests(guests map[string]string) string {
	names := make([]string, 0, len(guests))
	for n := range guests {
		names = append(names, n)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, n+"="+guests[n])
	}
	return strings.Join(pairs, ",")
}

// The access of a namespace is asked on every request of an account that
// is not an admin, so a reading is kept for a few seconds.
var (
	accessCacheMu sync.Mutex
	accessCache   = map[string]struct {
		access NamespaceAccess
		at     time.Time
	}{}
)

func forgetAccess(namespace string) {
	accessCacheMu.Lock()
	delete(accessCache, namespace)
	accessCacheMu.Unlock()
}

// ReadNamespaceAccess is the owner and guests of a namespace, from the
// object, a few seconds old at most.
func ReadNamespaceAccess(namespace string) (NamespaceAccess, error) {
	accessCacheMu.Lock()
	if c, ok := accessCache[namespace]; ok && time.Since(c.at) < accessCacheFor {
		accessCacheMu.Unlock()
		return c.access, nil
	}
	accessCacheMu.Unlock()
	ns, err := stateNamespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		return NamespaceAccess{}, err
	}
	a := ParseNamespaceAccess(ns)
	accessCacheMu.Lock()
	accessCache[namespace] = struct {
		access NamespaceAccess
		at     time.Time
	}{a, time.Now()}
	accessCacheMu.Unlock()
	return a, nil
}

// LevelOf is the level an account holds in a namespace: the owner level
// for admins, the owner account and the namespace's owner, a guest's own
// level, "" for no access at all.
func LevelOf(access NamespaceAccess, p *auth.Principal) string {
	if p == nil || p.IsAdmin() {
		return auth.LevelOwner
	}
	if access.Owner != "" && access.Owner == p.Identity {
		return auth.LevelOwner
	}
	return access.Guests[p.Identity]
}

// LevelForNamespace reads the namespace and tells the account's level in
// it. A namespace that is not there is no access, so a name is not told
// apart from a denial.
func LevelForNamespace(namespace string, p *auth.Principal) (string, error) {
	if p == nil || p.IsAdmin() {
		return auth.LevelOwner, nil
	}
	access, err := ReadNamespaceAccess(namespace)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return LevelOf(access, p), nil
}

func patchNamespaceAnnotations(namespace string, annotations map[string]*string) error {
	fields := make([]string, 0, len(annotations))
	for k, v := range annotations {
		if v == nil {
			fields = append(fields, fmt.Sprintf("%q: null", k))
		} else {
			fields = append(fields, fmt.Sprintf("%q: %q", k, *v))
		}
	}
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{%s}}}`, strings.Join(fields, ",")))
	if _, err := stateNamespaces().Patch(context.TODO(), namespace, k8stypes.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("could not write the access of namespace %s: %w", namespace, err)
	}
	forgetAccess(namespace)
	return nil
}

// SetNamespaceOwner names the account a namespace belongs to.
func SetNamespaceOwner(namespace, owner string) error {
	return patchNamespaceAnnotations(namespace, map[string]*string{OwnerAnnotation: &owner})
}

// SetNamespaceGuests replaces the guests of a namespace with the given
// name to level pairs, every level a valid one.
func SetNamespaceGuests(namespace string, guests map[string]string) error {
	for name, level := range guests {
		if !auth.ValidLevel(level) {
			return fmt.Errorf("%s: a guest's level is viewer, operator or editor, not %q", name, level)
		}
	}
	value := formatGuests(guests)
	var v *string
	if value != "" {
		v = &value
	}
	return patchNamespaceAnnotations(namespace, map[string]*string{AccessAnnotation: v})
}

// RemoveAccountFromNamespaces takes a deleted account out of every
// namespace: the ones it owned go to newOwner, or to nobody (the admins')
// when newOwner is "", and it leaves every guest list. Returns how many it
// owned and how many it was a guest of.
func RemoveAccountFromNamespaces(name, newOwner string) (owned, guested int, err error) {
	list, err := stateNamespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: "kubendt/enabled=true"})
	if err != nil {
		return 0, 0, fmt.Errorf("could not list the namespaces: %w", err)
	}
	for i := range list.Items {
		ns := &list.Items[i]
		access := ParseNamespaceAccess(ns)
		patch := map[string]*string{}
		guestsChanged := false
		if access.Owner == name {
			owned++
			var v *string
			if newOwner != "" {
				v = &newOwner
				// The new owner is nobody's guest.
				if _, wasGuest := access.Guests[newOwner]; wasGuest {
					delete(access.Guests, newOwner)
					guestsChanged = true
				}
			}
			patch[OwnerAnnotation] = v
		}
		if _, isGuest := access.Guests[name]; isGuest {
			guested++
			delete(access.Guests, name)
			guestsChanged = true
		}
		if guestsChanged {
			var v *string
			if value := formatGuests(access.Guests); value != "" {
				v = &value
			}
			patch[AccessAnnotation] = v
		}
		if len(patch) == 0 {
			continue
		}
		if err := patchNamespaceAnnotations(ns.Name, patch); err != nil {
			return owned, guested, err
		}
	}
	return owned, guested, nil
}
