package capabilities

import (
	"errors"
	"testing"
)

func TestParseIPRouteBusyBoxAndIproute2(t *testing.T) {
	text := "default via 172.22.48.1 dev eth1 \n10.244.2.0/24 dev eth0 scope link  src 10.244.2.150 \nunreachable 192.0.2.0/24 metric 1024\n"
	rows := parseIPRoute(text, "4")
	if len(rows) != 3 || rows[0]["destination"] != "0.0.0.0/0" || rows[0]["gateway"] != "172.22.48.1" || rows[0]["interface"] != "eth1" {
		t.Errorf("default route: %v", rows)
	}
	if rows[1]["destination"] != "10.244.2.0/24" || rows[1]["gateway"] != "" || rows[1]["interface"] != "eth0" {
		t.Errorf("connected route: %v", rows[1])
	}
	if rows[2]["destination"] != "192.0.2.0/24" || rows[2]["metric"] != "1024" {
		t.Errorf("unreachable route: %v", rows[2])
	}
	v6 := parseIPRoute("fe80::/64 dev eth0  metric 256 \ndefault via fd00::1 dev eth1 metric 1024 pref medium\n", "6")
	if len(v6) != 2 || v6[1]["destination"] != "::/0" || v6[1]["gateway"] != "fd00::1" || v6[0]["family"] != "6" {
		t.Errorf("v6 routes: %v", v6)
	}
}

func TestParseIPNeigh(t *testing.T) {
	rows := parseIPNeigh("172.22.48.1 dev eth1 lladdr fe:6d:2a:97:bb:bd used 0/0/0 probes 1 STALE\n172.22.17.1 dev eth1 lladdr ce:77:8b:91:55:e5 STALE proto zebra\nfe80::1 dev eth1 lladdr 02:00:00:00:00:01 router REACHABLE\nfe80::2 dev eth0 lladdr 02:00:00:00:00:02 STALE\n")
	if len(rows) != 3 || rows[0]["mac"] != "fe:6d:2a:97:bb:bd" || rows[0]["state"] != "STALE" || rows[1]["state"] != "STALE" || rows[2]["state"] != "REACHABLE" || rows[2]["interface"] != "eth1" {
		t.Errorf("neighbours: %v", rows)
	}
}

func TestParseBridgeFDBAndNATRules(t *testing.T) {
	rows := parseBridgeFDB("33:33:00:00:00:01 dev eth1 self permanent\n02:42:ac:11:00:02 dev eth1 vlan 1 master br0 \n02:42:ac:11:00:03 dev eth2 master br0 permanent\n")
	if len(rows) != 2 || rows[0]["bridge"] != "br0" || rows[0]["vlan"] != "1" || rows[0]["flags"] != "" || rows[1]["flags"] != "permanent" || rows[1]["interface"] != "eth2" {
		t.Errorf("fdb: %v", rows)
	}
	nat := parseNATRules("-P PREROUTING ACCEPT\n-A POSTROUTING -o eth0 -j MASQUERADE\n-A PREROUTING -i eth1 -p tcp -m tcp --dport 80 -j DNAT --to-destination 10.0.0.5:80\n", "4")
	if len(nat) != 2 || nat[0]["chain"] != "POSTROUTING" || nat[0]["rule"] != "-o eth0 -j MASQUERADE" || nat[1]["family"] != "4" {
		t.Errorf("nat rules: %v", nat)
	}
}

const frrRouteSample = `{"0.0.0.0/0":[{"protocol":"kernel","selected":true,"installed":true,"distance":0,"metric":0,"nexthops":[{"ip":"10.244.2.1","interfaceName":"eth0","active":true}]}],
"fe80::/64":[{"protocol":"connected","distance":0,"metric":0,"nexthops":[{"interfaceName":"eth1","active":true}]}],
"172.22.16.0/26":[{"protocol":"rip","selected":true,"distance":120,"metric":2,"nexthops":[{"ip":"172.22.50.197","interfaceName":"eth5","active":true},{"ip":"172.22.50.198","interfaceName":"eth6","active":true}]}],
"172.22.17.0/30":[{"protocol":"connected","selected":true,"distance":0,"metric":0,"nexthops":[{"interfaceName":"eth2","active":true}]}]}`

func TestParseFRRRoutes(t *testing.T) {
	rows, err := parseFRRRoutes([]byte(frrRouteSample), "4")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 || rows[0]["destination"] != "0.0.0.0/0" || rows[0]["protocol"] != "kernel" || rows[0]["status"] != "" {
		t.Errorf("routes: %v", rows)
	}
	if rows[1]["protocol"] != "rip" || rows[1]["distance"] != "120" || rows[1]["metric"] != "2" || rows[1]["gateway"] != "172.22.50.197" || rows[2]["gateway"] != "172.22.50.198" || rows[2]["interface"] != "eth6" {
		t.Errorf("ecmp rip route: %v %v", rows[1], rows[2])
	}
	if rows[3]["protocol"] != "connected" || rows[3]["gateway"] != "" || rows[3]["interface"] != "eth2" {
		t.Errorf("connected: %v", rows[3])
	}
	if rows[4]["destination"] != "fe80::/64" || rows[4]["status"] != "not installed" {
		t.Errorf("a route zebra did not select: %v", rows[4])
	}
	if empty, err := parseFRRRoutes([]byte("  \n"), "6"); err != nil || len(empty) != 0 {
		t.Errorf("empty table: %v %v", empty, err)
	}
}

const frrOSPFNeighborSample = `{"neighbors":{"172.22.19.3":[{"nbrState":"Full\/DR","nbrPriority":1,"converged":"Full","role":"DR","deadTime":"30.425s","ifaceAddress":"172.22.17.10","ifaceName":"eth2:172.22.17.9"}],
"172.22.19.1":[{"nbrState":"Full\/-","nbrPriority":1,"converged":"Full","role":"DROther","deadTime":"39.000s","ifaceAddress":"172.22.17.1","ifaceName":"eth1:172.22.17.2"}]}}`

func TestParseFRROSPFNeighbors(t *testing.T) {
	rows, err := parseFRROSPFNeighbors([]byte(frrOSPFNeighborSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["neighbor"] != "172.22.19.1" || rows[0]["interface"] != "eth1" || rows[0]["state"] != "Full" || rows[0]["role"] != "DROther" {
		t.Errorf("first neighbour: %v", rows)
	}
	if rows[1]["neighbor"] != "172.22.19.3" || rows[1]["role"] != "DR" || rows[1]["address"] != "172.22.17.10" || rows[1]["dead_time"] != "30.425s" || rows[1]["priority"] != "1" {
		t.Errorf("second neighbour: %v", rows[1])
	}
}

const frrOSPFInterfaceSample = `{"interfaces":{"eth1":{"ifUp":true,"ipAddress":"172.22.17.2","ipAddressPrefixlen":30,"area":"0.0.0.0","networkType":"POINTOPOINT","cost":10,"state":"Point-To-Point","priority":1,"nbrAdjacentCount":1},
"eth3":{"ifUp":true,"ipAddress":"172.22.16.1","ipAddressPrefixlen":26,"area":"0.0.0.1","networkType":"BROADCAST","cost":10,"state":"DR","priority":255,"nbrAdjacentCount":0,"timerPassiveIface":true}}}`

func TestParseFRROSPFInterfaces(t *testing.T) {
	rows, err := parseFRROSPFInterfaces([]byte(frrOSPFInterfaceSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["interface"] != "eth1" || rows[0]["address"] != "172.22.17.2/30" || rows[0]["network_type"] != "pointopoint" || rows[0]["neighbors"] != "1" {
		t.Errorf("eth1: %v", rows)
	}
	if rows[1]["area"] != "0.0.0.1" || rows[1]["state"] != "DR" || rows[1]["priority"] != "255" || rows[1]["cost"] != "10" || rows[1]["passive"] != "yes" || rows[0]["passive"] != "" {
		t.Errorf("eth3: %v", rows[1])
	}
}

const frrBGPSummarySample = `{"ipv4Unicast":{"routerId":"10.255.0.1","as":65001,"peers":{"10.0.0.2":{"remoteAs":65002,"state":"Established","pfxRcd":3,"pfxSnt":4,"peerUptime":"00:10:11"}}},
"ipv6Unicast":{"routerId":"10.255.0.1","as":65001,"peers":{"fd00::2":{"remoteAs":65003,"state":"Active","pfxRcd":0,"pfxSnt":0,"peerUptime":"never"}}}}`

func TestParseFRRBGPSummary(t *testing.T) {
	rows, err := parseFRRBGPSummary([]byte(frrBGPSummarySample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["family"] != "4" || rows[0]["neighbor"] != "10.0.0.2" || rows[0]["remote_as"] != "65002" || rows[0]["state"] != "Established" || rows[0]["prefixes_received"] != "3" || rows[0]["uptime"] != "00:10:11" {
		t.Errorf("v4 peer: %v", rows)
	}
	if rows[1]["family"] != "6" || rows[1]["state"] != "Active" {
		t.Errorf("v6 peer: %v", rows[1])
	}
}

const frrISISNeighborSample = `{"areas":[{"area":"kubendt","circuits":[{"circuit":0},{"circuit":0,"adj":"0000.0000.0002","interface":"eth2","level":2,"state":"Up","expires-in":"29s","snpa":"2020.2020.2020"},{"circuit":0,"adj":"0000.0000.0002","interface":{"name":"eth3","state":"Up","level":2,"expires-in":"28s"}}]}]}`

func TestParseFRRISISNeighbors(t *testing.T) {
	rows, err := parseFRRISISNeighbors([]byte(frrISISNeighborSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["neighbor"] != "0000.0000.0002" || rows[0]["interface"] != "eth2" || rows[0]["level"] != "2" || rows[0]["state"] != "Up" || rows[1]["hold_time"] != "28s" {
		t.Errorf("isis neighbours: %v", rows)
	}
}

const frrOSPF6NeighborSample = `{"neighbors":[{"neighborId":"10.255.0.2","priority":1,"deadTime":"00:00:33","state":"Full","interfaceName":"eth2","interfaceState":"PointToPoint"}]}`

func TestParseFRROSPF6Neighbors(t *testing.T) {
	rows, err := parseFRROSPF6Neighbors([]byte(frrOSPF6NeighborSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["neighbor"] != "10.255.0.2" || rows[0]["state"] != "Full" || rows[0]["interface"] != "eth2" || rows[0]["role"] != "PointToPoint" {
		t.Errorf("ospf6 neighbours: %v", rows)
	}
}

func TestVtyshError(t *testing.T) {
	if VtyshError([]byte("{}"), nil) != nil {
		t.Error("no error stays none")
	}
	err := VtyshError([]byte("bgpd is not running\n"), errors.New("exec error: command terminated with exit code 1"))
	var nr *DaemonNotRunningError
	if !errors.As(err, &nr) || nr.Daemon != "bgpd" {
		t.Errorf("stopped daemon: %v", err)
	}
	if err := VtyshError(nil, errors.New("exec error: exit status 1 | output: isisd is not running")); !errors.As(err, &nr) || nr.Daemon != "isisd" {
		t.Errorf("stopped daemon in the error text: %v", err)
	}
	other := errors.New("exec error: something else")
	if VtyshError([]byte("% Unknown command"), other) != other {
		t.Error("another failure comes back as it was")
	}
}
