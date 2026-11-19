// The file the topology export writes: what get-network returns, without the
// link uids unless asked, so the file deploys anywhere with fresh ones.
export const topologyFile = (data, { keepUids = false } = {}) => {
  if (keepUids || !Array.isArray(data?.links)) return data;
  return { ...data, links: data.links.map(({ uid, ...link }) => link) };
};
