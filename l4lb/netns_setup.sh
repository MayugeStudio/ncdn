#!/bin/bash

cd $(dirname $0)

if [ "$EUID" -ne 0 ]; then
    echo "Please run as root"
    exit
fi

function add_ns_bare() {
    ip netns del $1
    rm /var/run/netns/$1

    ip netns add $1
    ip -n $1 l set lo up

    # disable ipv6 on the netns - for cleaner tcpdump
    ip netns exec $1 sysctl -w net.ipv6.conf.all.disable_ipv6=1
    # disable rp_filter for direct server return (DSR)
    ip netns exec $1 sysctl -w net.ipv4.conf.all.rp_filter=0
    ip netns exec $1 sysctl -w net.ipv4.conf.default.rp_filter=0
}

function add_ns() {
    ip l del $1-net0

    ip netns del $1
    rm /var/run/netns/$1

    ip netns add $1
    ip -n $1 l set lo up

    ip l add net0 netns $1 type veth peer name $1-net0
    ip -n $1 l set net0 up
    ip l set $1-net0 master brDev
    ip l set $1-net0 up

    # disable TCO - while veth optimizes the TCP transports by
    # skipping checksum computation/verification altogether,
    # we actually need a good checksum since we're going to
    # encap the packets in IPIP tunnels. Otherwise the IPIP
    # receiver would drop the packets given all its bad csums.
    ip netns exec $1 ethtool --offload net0 tx off rx off

    # disable ipv6 on the netns - for cleaner tcpdump
    ip netns exec $1 sysctl -w net.ipv6.conf.all.disable_ipv6=1
    # disable rp_filter for direct server return (DSR)
    ip netns exec $1 sysctl -w net.ipv4.conf.all.rp_filter=0
    ip netns exec $1 sysctl -w net.ipv4.conf.default.rp_filter=0
    ip netns exec $1 sysctl -w net.ipv4.conf.net0.rp_filter=0

    ip -n $1 a add $2 dev net0
}

set -x
ip l add name brDev type bridge
ip l set dev brDev up

ip l add name brDev-out type bridge
ip l set dev brDev-out up

add_ns_bare U # "198.51.100.200/24"
add_ns R "192.168.88.1/24"
add_ns LB "192.168.88.20/24"
add_ns C0 "192.168.88.10/24"
add_ns C1 "192.168.88.11/24"
add_ns C2 "192.168.88.12/24"
add_ns C3 "192.168.88.13/24"
add_ns C4 "192.168.88.14/24"
add_ns O "192.168.88.30/24"

# Host <-> br-Dev
ip l del H-net0
ip l a host0 type veth peer name H-net0
ip a add 198.51.100.100/24 dev host0
ip l set host0 up
ethtool --offload host0 tx off rx off

ip l set H-net0 master brDev-out
ip l set H-net0 up

ip r add 192.0.2.0/24 via 198.51.100.1


# veth: U (198.51.100.200/24) <-> R (198.51.100.1/24)
ip l del U-net0
ip l a net0 netns U type veth peer name U-net0
ip -n U a add 198.51.100.200/24 dev net0
ip -n U l set net0 up
ip netns exec U ethtool --offload net0 tx off rx off

ip l set U-net0 master brDev-out
ip l set U-net0 up

ip l del R-net1
ip l a net1 netns R type veth peer name R-net1
ip -n R a add 198.51.100.1/24 dev net1
ip -n R l set net1 up
ip netns exec R ethtool --offload net1 tx off rx off

ip l set R-net1 master brDev-out
ip l set R-net1 up

# enable routing on R and LB
ip netns exec R sysctl -w net.ipv4.ip_forward=1
ip netns exec LB sysctl -w net.ipv4.ip_forward=1
ip netns exec C0 sysctl -w net.ipv4.ip_forward=1
ip netns exec C1 sysctl -w net.ipv4.ip_forward=1
ip netns exec C2 sysctl -w net.ipv4.ip_forward=1
ip netns exec C3 sysctl -w net.ipv4.ip_forward=1
ip netns exec C4 sysctl -w net.ipv4.ip_forward=1

# default route to R
ip -n U r add default via 198.51.100.1
ip -n LB r add default via 192.168.88.1
ip -n C0 r add default via 192.168.88.1
ip -n C1 r add default via 192.168.88.1
ip -n C2 r add default via 192.168.88.1
ip -n C3 r add default via 192.168.88.1
ip -n C4 r add default via 192.168.88.1
# ip netns exec O ip r add default via 192.168.88.1

# LB->C0 ipip tunnel - claim VIP
ip -n C0 tunnel a ipip0 remote 192.168.88.20 local 192.168.88.10 dev net0
ip -n C0 l set ipip0 up
ip -n C0 a add 192.0.2.10/32 dev ipip0

# LB->C1 ipip tunnel - claim VIP
ip -n C1 tunnel a ipip0 remote 192.168.88.20 local 192.168.88.11 dev net0
ip -n C1 l set ipip0 up
ip -n C1 a add 192.0.2.10/32 dev ipip0

# LB->C2 ipip tunnel - claim VIP
ip -n C2 tunnel a ipip0 remote 192.168.88.20 local 192.168.88.12 dev net0
ip -n C2 l set ipip0 up
ip -n C2 a add 192.0.2.10/32 dev ipip0

# LB->C3 ipip tunnel - claim VIP
ip -n C3 tunnel a ipip0 remote 192.168.88.20 local 192.168.88.13 dev net0
ip -n C3 l set ipip0 up
ip -n C3 a add 192.0.2.10/32 dev ipip0

# LB->C4 ipip tunnel - claim VIP
ip -n C4 tunnel a ipip0 remote 192.168.88.20 local 192.168.88.14 dev net0
ip -n C4 l set ipip0 up
ip -n C4 a add 192.0.2.10/32 dev ipip0

# Route VIPs to LB
ip -n R r add 192.0.2.0/24 via 192.168.88.20

# inject dummy xdp prog to workaround https://www.spinics.net/lists/netdev/msg625217.html
(cd c && make dummy.o)
ip l set dev LB-net0 xdp obj c/dummy.o sec xdp
