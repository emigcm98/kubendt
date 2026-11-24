package auth

// What a request needs and what a level grants. A permission names an act on
// a namespace (to see it, to work on its pods with the tools, to change it,
// to own it) or on the platform; a level inside a namespace is a set of
// them, a ladder only by habit. The route table of the handlers names one
// permission per route, and the middleware asks whether the account's level
// in the namespace of the request, or its platform role, grants it.
type Permission string

const (
	// PermAny is any signed-in account.
	PermAny Permission = "any"
	// PermView reads the namespace: its pods, links, files, history.
	PermView Permission = "namespace:view"
	// PermOperate works on the pods without changing the lab: shells,
	// captures, traceroutes, throughput tests.
	PermOperate Permission = "namespace:operate"
	// PermEdit changes the lab: deploy, modify, configure, restart, files,
	// layout, history.
	PermEdit Permission = "namespace:edit"
	// PermOwn disposes of the lab: delete it, clear it, share it.
	PermOwn Permission = "namespace:own"
	// PermCreateNamespace makes a namespace of one's own: an admin, or a
	// user with the flag.
	PermCreateNamespace Permission = "namespace:create"
	// PermAdmin is the platform: the cluster, the kubeconfig, every
	// namespace, the users.
	PermAdmin Permission = "platform:admin"
	// PermOwner is the owner account alone: naming and demoting admins.
	PermOwner Permission = "platform:owner"
)

// The levels an account may hold inside a namespace, from the annotation
// on the Namespace object; LevelOwner is the namespace's owner, and what
// admins and the owner account hold everywhere.
const (
	LevelViewer   = "viewer"
	LevelOperator = "operator"
	LevelEditor   = "editor"
	LevelOwner    = "owner"
)

var levelGrants = map[string]map[Permission]bool{
	LevelViewer:   {PermView: true},
	LevelOperator: {PermView: true, PermOperate: true},
	LevelEditor:   {PermView: true, PermOperate: true, PermEdit: true},
	LevelOwner:    {PermView: true, PermOperate: true, PermEdit: true, PermOwn: true},
}

// ValidLevel says whether a level can be given to a guest.
func ValidLevel(level string) bool {
	return level == LevelViewer || level == LevelOperator || level == LevelEditor
}

// LevelGrants says whether a level inside a namespace grants a permission
// on it. Platform permissions are never a level's.
func LevelGrants(level string, perm Permission) bool {
	return levelGrants[level][perm]
}

// GrantsPlatform says whether the account's role grants a platform
// permission, or any permission at all for admins and the owner, who hold
// the owner level in every namespace.
func (p *Principal) GrantsPlatform(perm Permission) bool {
	if p == nil {
		return false
	}
	switch perm {
	case PermAny:
		return true
	case PermOwner:
		return p.Role == RoleOwner
	case PermAdmin:
		return p.IsAdmin()
	case PermCreateNamespace:
		if p.IsAdmin() {
			return true
		}
		u, ok := GetUser(p.Identity)
		return ok && u.CanCreateNamespaces
	}
	return p.IsAdmin()
}
