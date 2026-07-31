interfaces {
    __ETH0_BLOCK__
    ethernet __MGMT_GUEST_IFACE__ {
        address "__MGMT_VM_IP__"
        description "kubendt internal management (ssh_qemu)"
        hw-id "__MGMT_NIC_MAC__"
        offload {
            gro
            gso
            sg
            tso
        }
    }
    __INTERFACES_BLOCK__
    loopback lo {
    }
}
service {
    https {
        api {
            keys {
                id kubendt {
                    key "__API_KEY__"
                }
            }
            rest {
            }
        }
        listen-address "__MGMT_VM_IP_NO_CIDR__"
        port "443"
    }
    ssh {
        listen-address "__MGMT_VM_IP_NO_CIDR__"
        port "22"
        disable-password-authentication
        disable-host-validation
    }
    ntp {
        allow-client {
            address "127.0.0.0/8"
            address "169.254.0.0/16"
            address "10.0.0.0/8"
            address "172.16.0.0/12"
            address "192.168.0.0/16"
            address "::1/128"
            address "fe80::/10"
            address "fc00::/7"
        }
        server time1.vyos.net {
        }
        server time2.vyos.net {
        }
        server time3.vyos.net {
        }
    }
}
protocols {
    static {
        route 0.0.0.0/0 {
            next-hop __DEFAULT_GW__ {
            }
        }
    }
}
system {
    config-management {
        commit-revisions "100"
    }
    conntrack {
        modules {
            ftp
            h323
            nfs
            pptp
            sip
            sqlnet
            tftp
        }
    }
    console {
        device ttyS0 {
            speed "115200"
        }
    }
    host-name "__HOSTNAME__"
    __NAMESERVERS_BLOCK__
    domain-name "kubendt.local"
    login {
        operator-group default {
            command-policy {
                allow "*"
            }
        }
        user __USER__ {
            authentication {
                encrypted-password "__ENCRYPTED_PASSWORD__"
                plaintext-password ""
                public-keys mgmt {
                    key "__SSH_PUBLIC_KEY__"
                    type "ssh-ed25519"
                }
            }
        }
        banner {
            post-login "KubeNDT VyOS router"
        }
    }
    option {
        reboot-on-upgrade-failure "5"
    }
    syslog {
        local {
            facility all {
                level "info"
            }
            facility local7 {
                level "debug"
            }
        }
    }
}

// Warning: Do not remove the following line.
// vyos-config-version: "bgp@6:broadcast-relay@1:cluster@2:config-management@1:conntrack@6:conntrack-sync@2:container@3:dhcp-relay@2:dhcp-server@11:dhcpv6-server@6:dns-dynamic@4:dns-forwarding@4:firewall@20:flow-accounting@3:https@7:ids@2:interfaces@34:ipoe-server@4:ipsec@14:isis@3:l2tp@9:lldp@3:mdns@1:monitoring@2:nat@8:nat66@3:nhrp@1:ntp@3:openconnect@3:openvpn@5:ospf@2:pim@1:policy@9:pppoe-server@12:pptp@5:qos@3:quagga@12:reverse-proxy@3:rip@1:rpki@2:salt@1:snmp@3:ssh@3:sstp@6:system@31:vpp@6:vrf@4:vrrp@4:vyos-accel-ppp@2:wanloadbalance@4:webproxy@2"
// Release version: 2026.03.08-0026-rolling