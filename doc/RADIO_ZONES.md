# Radio zones

A radio zone is an area of the world prepared as a radio scenario: a 3D scene of its buildings, its road
network for moving nodes, the sites of a radio access network and a ray-traced coverage map per sector.
The dashboard page **Radio zones** (`/radio`) creates them on an OpenStreetMap map:

1. **Area.** Search a place and drag a rectangle (up to 3000 m per side). The buildings and roads of the
   area are fetched from OpenStreetMap.
2. **Sites.** Click on the map to add a site and drag it to move it; set its height (empty: the highest
   rooftop within 30 m plus a mast), its sector azimuths, downtilt and power. *Real masts (OSM)* adds the
   telephony masts mapped in OpenStreetMap inside the area, *Import CSV* an inventory of your own
   (`name, lat, lon, height_m, azimuths`), *Auto-place* spreads a number of sites over the built-up area.
3. **Build.** Extrudes the buildings into the scene (mapped height, or levels, or a default by building
   type), converts the roads to a SUMO network with background traffic, and traces the coverage maps.
   The result is drawn over the map. *Use for emulation* marks the zone the emulation loop reads.

## The radio service

Building needs a GPU and Sionna RT, so it runs outside the backend, in the radio service
(`radio_service.py`), on a host with a GPU. The backend forwards `/radio/*` to it when
`KUBENDT_RADIO_URL` is set and answers 503 otherwise. The service has no authentication: the backend's
session or token guards it, the caller's credentials are not forwarded, and the service must listen only
where the backend reaches it (the Docker network gateway in a compose deployment), never on a public
address.

| Route | |
| --- | --- |
| `GET /radio/zones`, `POST /radio/zones` `{name, bbox:[south, west, north, east]}` | list, create (fetches the map) |
| `GET /radio/zones/{id}`, `DELETE /radio/zones/{id}` | status and statistics, delete |
| `PUT /radio/zones/{id}/sites` `{sites}` | set the sites |
| `POST /radio/zones/{id}/sites/auto` `{count}`, `GET /radio/zones/{id}/sites/osm` | proposals, mapped masts |
| `POST /radio/zones/{id}/build`, `GET /radio/zones/{id}/coverage.png` | build, coverage overlay |
| `POST /radio/zones/{id}/activate`, `GET /radio/active` | the zone the emulation uses |

## Limits

Heights come from OpenStreetMap where mapped; elsewhere a default by building type stands in, so roofs
and street canyons are approximate. Terrain is flat. Mapped telephony masts are sparse in most cities:
an operator or regulator inventory through the CSV import is the reliable source of real positions.
