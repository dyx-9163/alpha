global_defs {
    router_id @ROUTER_ID@
    script_user root
    enable_script_security
}

vrrp_script check_aifar_health {
    script "/aifar/apps/keepalived/libexec/check-aggregate-health.sh"
    interval 2
    timeout 3
    fall 3
    rise 2
    weight 0
}

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
    track_script {
        check_aifar_health
    }
}
