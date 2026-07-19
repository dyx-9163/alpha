global_defs {
    router_id @ROUTER_ID@
@SCRIPT_SECURITY@
}

@HEALTH_SCRIPT@
vrrp_instance AIFAR_VI {
    state BACKUP
    interface @INTERFACE@
    virtual_router_id @VIRTUAL_ROUTER_ID@
    priority @PRIORITY@
    advert_int 1
    unicast_src_ip @LOCAL_IP@
    unicast_peer {
        @PEER_IP@
    }
    virtual_ipaddress {
        @VIP_CIDR@ dev @INTERFACE@
    }
@TRACK_SCRIPT@
}
