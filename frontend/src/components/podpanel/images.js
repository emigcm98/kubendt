// Mirrors helpers/images.go in the backend: a reference split into its name
// and its version, a tag or a digest, so `alpine:3.24.2` and
// `docker.io/library/alpine:3.24.2` meet and a missing tag reads as latest.
export const imageRef = (ref) => {
  let name = String(ref || '').trim();
  let version = ':latest';
  const at = name.indexOf('@');
  if (at >= 0) {
    version = `@${name.slice(at + 1)}`;
    name = name.slice(0, at);
  } else {
    const colon = name.lastIndexOf(':');
    if (colon > name.lastIndexOf('/')) {
      version = `:${name.slice(colon + 1)}`;
      name = name.slice(0, colon);
    }
  }
  const slash = name.indexOf('/');
  const first = slash < 0 ? name : name.slice(0, slash);
  const rest = slash < 0 ? '' : name.slice(slash + 1);
  if (slash < 0) name = `docker.io/library/${name}`;
  else if (first === 'docker.io' && !rest.includes('/')) name = `docker.io/library/${rest}`;
  else if (!/[.:]/.test(first) && first !== 'localhost') name = `docker.io/${name}`;
  return { name, version };
};

// Whether image is one of the known references: the same name and tag, or
// the same digest. A tag never matches a digest.
export const imageKnown = (known, image) => {
  const { name, version } = imageRef(image);
  return (known || []).some((k) => {
    const r = imageRef(k);
    return r.name === name && r.version === version;
  });
};
