package drivers_meta

// Meta provides getters Name()/Type() with private backing fields.
type Meta struct {
	name string
	typ  string
}

func NewMeta(name, typ string) Meta { return Meta{name: name, typ: typ} }

func (m Meta) Name() string { return m.name }
func (m Meta) Type() string { return m.typ }
