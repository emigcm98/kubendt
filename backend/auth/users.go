package auth

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// The accounts and what each may do to the others. The owner account is
// `admin`, the one every install has from the first start, so a login that
// names no user and every script keep working; its role is `owner`. An
// admin manages users and never touches the owner or another admin; the
// owner names and demotes admins and cannot be disabled or deleted.
const (
	OwnerAccount = "admin"
	RoleOwner    = "owner"
	RoleAdmin    = "admin"
	RoleUser     = "user"
	SourceLocal  = "local"
)

var (
	ErrUserExists   = errors.New("a user with that name already exists")
	ErrUserNotFound = errors.New("no such user")
	ErrBadUsername  = errors.New("a username is one to thirty-one lowercase letters, digits or dashes, starting with a letter or a digit")
	ErrBadRole      = errors.New("the role is owner, admin or user")
	ErrNotAllowed   = errors.New("not allowed for this account")
	ErrBadPassword  = errors.New("the password does not match")
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// User is an account as the API shows it, never its hash.
type User struct {
	Name                string `json:"username"`
	Role                string `json:"role"`
	Disabled            bool   `json:"disabled"`
	MustChangePassword  bool   `json:"must_change_password"`
	CanCreateNamespaces bool   `json:"can_create_namespaces"`
	QuotaNamespaces     int    `json:"quota_namespaces"`
	QuotaPods           int    `json:"quota_pods"`
	Source              string `json:"source"`
	CreatedAt           int64  `json:"created_at"`
}

// UserPatch is what an update may change; a nil field is left alone.
type UserPatch struct {
	Role                *string `json:"role"`
	Disabled            *bool   `json:"disabled"`
	CanCreateNamespaces *bool   `json:"can_create_namespaces"`
	QuotaNamespaces     *int    `json:"quota_namespaces"`
	QuotaPods           *int    `json:"quota_pods"`
}

func (u *userRecord) view() User {
	return User{Name: u.Name, Role: u.Role, Disabled: u.Disabled, MustChangePassword: u.MustChangePassword,
		CanCreateNamespaces: u.CanCreateNamespaces, QuotaNamespaces: u.QuotaNamespaces, QuotaPods: u.QuotaPods,
		Source: u.Source, CreatedAt: u.CreatedAt}
}

func validRole(role string) bool {
	return role == RoleOwner || role == RoleAdmin || role == RoleUser
}

// IsAdmin says the principal manages users: an admin or the owner.
func (p *Principal) IsAdmin() bool { return p != nil && (p.Role == RoleAdmin || p.Role == RoleOwner) }

// mayManage says whether actor may change target: the owner anyone but its
// own role and standing, an admin only plain users, a user nobody.
func mayManage(actor *Principal, target *userRecord) bool {
	if actor == nil {
		return false
	}
	switch actor.Role {
	case RoleOwner:
		return target.Name != OwnerAccount
	case RoleAdmin:
		return target.Role == RoleUser
	}
	return false
}

func hashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// ListUsers is every account, the owner first then by name.
func ListUsers() ([]User, error) {
	if !IsBound() {
		return []User{}, nil
	}
	if err := reloadStore(); err != nil {
		return nil, err
	}
	mu.RLock()
	out := make([]User, 0, len(users))
	for _, u := range users {
		out = append(out, u.view())
	}
	mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == OwnerAccount) != (out[j].Name == OwnerAccount) {
			return out[i].Name == OwnerAccount
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// GetUser is one account as the API shows it.
func GetUser(name string) (*User, bool) {
	refreshStoreIfStale()
	mu.RLock()
	defer mu.RUnlock()
	u, ok := users[name]
	if !ok {
		return nil, false
	}
	v := u.view()
	return &v, true
}

// CreateUser adds an account. An empty password is generated and returned,
// to be shown once; a created user changes it at the first login either way.
// Only the owner creates admins.
func CreateUser(actor *Principal, name, password, role string, canCreate bool, quotaNamespaces, quotaPods int) (generated string, err error) {
	if !IsBound() {
		return "", ErrNoStore
	}
	name = strings.TrimSpace(name)
	if !usernameRe.MatchString(name) {
		return "", ErrBadUsername
	}
	if role == "" {
		role = RoleUser
	}
	if !validRole(role) || role == RoleOwner {
		return "", ErrBadRole
	}
	if !actor.IsAdmin() || (role == RoleAdmin && actor.Role != RoleOwner) {
		return "", ErrNotAllowed
	}
	if err := reloadStore(); err != nil {
		return "", err
	}
	mu.RLock()
	_, exists := users[name]
	mu.RUnlock()
	if exists {
		return "", ErrUserExists
	}
	if password == "" {
		if generated, err = randomToken(12); err != nil {
			return "", err
		}
		password = generated
	}
	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}
	u := &userRecord{Name: name, Hash: hash, Role: role, MustChangePassword: true, CanCreateNamespaces: canCreate,
		QuotaNamespaces: quotaNamespaces, QuotaPods: quotaPods, Source: SourceLocal, CreatedAt: nowUnix()}
	if err := writeUser(u); err != nil {
		return "", err
	}
	mu.Lock()
	users[name] = u
	mu.Unlock()
	return generated, nil
}

// UpdateUser changes an account's role, standing, flags or quotas. A user
// disabled has its sessions ended; a role given by an admin is never admin.
func UpdateUser(actor *Principal, name string, patch UserPatch) error {
	if !IsBound() {
		return ErrNoStore
	}
	if err := reloadStore(); err != nil {
		return err
	}
	mu.RLock()
	current, ok := users[name]
	mu.RUnlock()
	if !ok {
		return ErrUserNotFound
	}
	if !mayManage(actor, current) {
		return ErrNotAllowed
	}
	u := *current
	if patch.Role != nil {
		if !validRole(*patch.Role) || *patch.Role == RoleOwner {
			return ErrBadRole
		}
		if *patch.Role != u.Role && actor.Role != RoleOwner {
			return ErrNotAllowed
		}
		u.Role = *patch.Role
	}
	if patch.Disabled != nil {
		u.Disabled = *patch.Disabled
		if u.Disabled {
			u.SessionsValidAfter = nowUnix()
		}
	}
	if patch.CanCreateNamespaces != nil {
		u.CanCreateNamespaces = *patch.CanCreateNamespaces
	}
	if patch.QuotaNamespaces != nil {
		u.QuotaNamespaces = *patch.QuotaNamespaces
	}
	if patch.QuotaPods != nil {
		u.QuotaPods = *patch.QuotaPods
	}
	if err := writeUser(&u); err != nil {
		return err
	}
	mu.Lock()
	users[name] = &u
	mu.Unlock()
	return nil
}

// DeleteUser removes an account and its tokens. The owner stays.
func DeleteUser(actor *Principal, name string) error {
	if !IsBound() {
		return ErrNoStore
	}
	if err := reloadStore(); err != nil {
		return err
	}
	mu.RLock()
	current, ok := users[name]
	mu.RUnlock()
	if !ok {
		return ErrUserNotFound
	}
	if !mayManage(actor, current) {
		return ErrNotAllowed
	}
	mu.RLock()
	var owned []*tokenEntry
	for _, t := range tokens {
		if t.User == name {
			owned = append(owned, t)
		}
	}
	mu.RUnlock()
	for _, t := range owned {
		if _, err := deleteToken(t.ID); err != nil {
			return err
		}
	}
	if err := deleteUserSecret(name); err != nil {
		return err
	}
	mu.Lock()
	delete(users, name)
	for h, t := range tokens {
		if t.User == name {
			delete(tokens, h)
		}
	}
	mu.Unlock()
	return nil
}

// ResetPassword gives an account a new password, the given one or one
// generated and returned, to change at the next login; its sessions end.
func ResetPassword(actor *Principal, name, password string) (generated string, err error) {
	if !IsBound() {
		return "", ErrNoStore
	}
	if err := reloadStore(); err != nil {
		return "", err
	}
	mu.RLock()
	current, ok := users[name]
	mu.RUnlock()
	if !ok {
		return "", ErrUserNotFound
	}
	if !mayManage(actor, current) {
		return "", ErrNotAllowed
	}
	if password == "" {
		if generated, err = randomToken(12); err != nil {
			return "", err
		}
		password = generated
	}
	u := *current
	if u.Hash, err = hashPassword(password); err != nil {
		return "", err
	}
	u.MustChangePassword, u.SessionsValidAfter = true, nowUnix()
	if err := writeUser(&u); err != nil {
		return "", err
	}
	mu.Lock()
	users[name] = &u
	mu.Unlock()
	return generated, nil
}

// ChangePassword is an account changing its own password: the current one
// must match, the sessions it had end, and the forced change is cleared.
func ChangePassword(name, current, next string) error {
	if strings.TrimSpace(next) == "" {
		return errors.New("the new password is empty")
	}
	refreshStoreIfStale()
	mu.RLock()
	u, ok := users[name]
	mu.RUnlock()
	if !ok {
		return ErrUserNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(current)) != nil {
		return ErrBadPassword
	}
	hash, err := hashPassword(next)
	if err != nil {
		return err
	}
	changed := *u
	changed.Hash, changed.MustChangePassword, changed.SessionsValidAfter = hash, false, nowUnix()
	if IsBound() {
		if err := writeUser(&changed); err != nil {
			return err
		}
	}
	mu.Lock()
	users[name] = &changed
	mu.Unlock()
	return nil
}
