# Radio zones

A radio zone is an area of the world prepared as a radio scenario and, from it, a KubeNDT namespace with a
5G system whose radio links are emulated. The dashboard page **Radio zones** (`/radio`) does it in five steps
on an OpenStreetMap map. The home page shows the zone in use and links to it; every namespace has a
**Radio area** button, and a namespace generated from a zone also has **Start radio / Stop radio**.

## The five steps

1. **Area.** Search a place and drag a rectangle (up to 3000 m per side). The buildings and roads of the area
   are fetched from OpenStreetMap.
2. **Sites.** Click on the map to add a site and drag it to move it. Each site has a height (empty: the
   highest rooftop within 30 m plus a mast), sector azimuths, a downtilt and a power. *Real masts (OSM)* adds
   the telephony masts mapped in OpenStreetMap inside the area, *Import CSV* an inventory of your own
   (`name, lat, lon, height_m, azimuths`), *Auto-place* spreads a number of sites over the built-up area.
3. **Build.** Extrudes the buildings into a 3D scene (mapped height, or levels, or a default by building
   type), converts the roads to a SUMO network with background traffic, and traces a coverage map per sector
   with Sionna RT. The coverage is drawn over the map.
4. **UEs.** Each UE is a car or a person and has a mode:
   - **mobile**: it moves on its own, from where you put it, to destinations SUMO picks at random;
   - **fixed**: it stays where you put it;
   - **route**: the first click is its start and the next ones its destinations, visited in that order;
     at the last one it stops, or loops back to the start and repeats.

   UEs go on streets only: a car snaps to the nearest carriageway, a person to the nearest pavement or
   footpath. *Auto-place* spreads a number of cars and persons over the streets the sites serve.
5. **Namespace and emulation.** *Create namespace* deploys the 5G system of the zone. *Start emulation* moves
   the UEs and drives the radio links; the map shows each UE with the trail it has travelled and the route it
   plans, and a table gives its serving gNB, SINR, rates and whether a real ping reaches the server.

## The generated namespace

| Nodes | |
| --- | --- |
| `amf`, `smf`, `nrf`, `scp`, `ausf`, `udm`, `udr`, `pcf`, `upf`, `udsf` | Open5GS core and its MongoDB, on one switch (`sw-0`) |
| `gnb-<i>` | one UERANSIM gNB per site, type `gnb` |
| `ue-<k>` | one UERANSIM UE per terminal, type `ue`, wired to every gNB |
| `dn-0`, `server-0` | the data network behind the UPF (10.45.0.0/24, gateway 10.45.0.1) and a first server (10.45.0.10) |

Hang your own servers from `dn-0`. The links between a UE and the gNBs are its radio links: the emulation
shapes the one to its serving gNB and cuts the others. Moving UEs or redrawing routes needs no new namespace;
changing the number of sites or of UEs marks it out of date, and *Regenerate* creates it again from scratch.

## The emulation

Every step (0.5 s): SUMO moves the mobile and route UEs and the background traffic; Sionna RT gives the gain
of each UE to each sector, with the nearby vehicles as moving objects, on top of the coverage map; an A3 rule
(L3 filter, hysteresis, time to trigger, radio link failure) picks the serving sector; the rate from the
SINR goes to both ends of the serving radio link as an in-place netem through the KubeNDT API.

## The radio service

Building and emulating need a GPU and Sionna RT, so they run outside the backend, in the radio service
(`radio/radio_service.py`), on a host with a GPU. The backend forwards `/radio/*` to it when
`KUBENDT_RADIO_URL` is set and answers 503 otherwise. The service has no authentication: the backend's session
or token guards it, the caller's credentials are not forwarded, and the service must listen only where the
backend reaches it, never on a public address. It talks back to KubeNDT with an API token. Where it listens, where KubeNDT is, its working folder, the token
and the container images are settings given when it is launched: see the README.

| Route | |
| --- | --- |
| `GET /radio/zones`, `POST /radio/zones` `{name, bbox:[south, west, north, east]}` | list, create (fetches the map) |
| `GET /radio/zones/{id}`, `DELETE /radio/zones/{id}` | status and statistics, delete |
| `PUT /radio/zones/{id}/sites`, `POST .../sites/auto`, `GET .../sites/osm` | sites, proposals, mapped masts |
| `POST /radio/zones/{id}/build`, `GET .../coverage.png` | build, coverage overlay |
| `PUT /radio/zones/{id}/ues`, `POST .../ues/auto`, `POST .../ues/snap` | UEs, automatic placement, nearest street |
| `POST /radio/zones/{id}/namespace` `{name}`, `DELETE .../namespace` | create or regenerate the namespace, delete it |
| `POST /radio/zones/{id}/emulation/start` `{takeover}`, `POST .../emulation/stop` | the emulation; `takeover` stops the one running in another zone |
| `GET /radio/namespaces/{namespace}` | the zone a namespace was generated from |
| `POST /radio/zones/{id}/activate`, `GET /radio/active` | the zone shown as in use |

## Limits

Heights come from OpenStreetMap where mapped; elsewhere a default by building type stands in, so roofs and
street canyons are approximate. Roofs and terrain are flat. Mapped telephony masts are sparse in most cities:
an operator or regulator inventory through the CSV import is the reliable source of real positions.
UERANSIM reselects the cell on a change of gNB instead of handing over, which interrupts service for a few
seconds; the emulator keeps the serving link at a 50 kbit/s floor during fades and restarts the 5G stack of a
UE whose user plane does not come back. One emulation runs at a time.
