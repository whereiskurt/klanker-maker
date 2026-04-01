// SPDX-License-Identifier: GPL-2.0-only
/*
 * sni.c — TC egress classifier for TLS SNI-based hostname filtering.
 *
 * Purpose:
 *   The cgroup/connect4 and cgroup_skb/egress programs enforce at the IP
 *   layer. This TC classifier adds a hostname-level layer by parsing TLS
 *   ClientHello SNI on port 443. It is intentionally best-effort:
 *   - Packets where SNI cannot be parsed (non-TLS, fragmented, SNI in 2nd
 *     segment) are ALWAYS passed through (TC_ACT_OK).
 *   - Only packets with a parseable SNI that is NOT in the allowed_sni map
 *     are dropped (TC_ACT_SHOT).
 *
 * Kernel compatibility:
 *   Uses classic cls_bpf via SEC("classifier/sni_filter"). This is compatible
 *   with AL2023 kernel 6.1. TCX requires kernel 6.6+ and is NOT used here.
 *
 * Build: go generate ./pkg/ebpf/sni/ (requires clang + Linux BPF headers)
 *
 * Requirement: EBPF-NET-10
 */

#include "../headers/vmlinux.h"
#include "../headers/bpf_helpers.h"

/* ════════════════════════════════════════════════════════════════════
 * Type aliases
 * ════════════════════════════════════════════════════════════════════ */
typedef unsigned char  u8;
typedef unsigned short u16;
typedef unsigned int   u32;
typedef unsigned long long u64;

/* ════════════════════════════════════════════════════════════════════
 * Constants
 * ════════════════════════════════════════════════════════════════════ */

/* TC return codes */
#define TC_ACT_OK    0   /* pass packet */
#define TC_ACT_SHOT  2   /* drop packet */

/* TLS/TCP parsing constants */
#define PORT_HTTPS   443
#define ETH_HDR_LEN  14
#define ETH_P_IP     0x0800
#define TLS_HANDSHAKE_CONTENT_TYPE  22
#define TLS_HANDSHAKE_CLIENT_HELLO   1
#define TLS_EXT_SNI                  0
#define SNI_HOSTNAME_TYPE            0

/* Maximum depth into TCP payload to search for SNI. Most ClientHellos fit
 * within 512 bytes of payload. Chrome hellos can exceed 1500 bytes (entire
 * MTU) — those will be TCP-segmented and SNI may land in segment 2, which
 * we cannot inspect. Best-effort: pass through if SNI not found. */
#define MAX_SNI_PARSE_DEPTH  512

/* Maximum SNI hostname length per RFC 6066 */
#define SNI_MAX_LEN  255

/* Key size for the allowed_sni map: null-padded to 256 bytes */
#define SNI_KEY_LEN  256

/* Maximum number of TLS extensions we will iterate over before giving up.
 * Bounded for BPF verifier compliance. */
#define MAX_TLS_EXTENSIONS  32

/* ════════════════════════════════════════════════════════════════════
 * BPF MAP DEFINITIONS
 * ════════════════════════════════════════════════════════════════════ */

/*
 * allowed_sni: set of permitted TLS SNI hostnames.
 * Key:   char[256] — null-padded lowercase hostname (exact match).
 * Value: u8(1)     — presence indicates hostname is allowed.
 * Populated by Go-side AllowSNI() from the profile's allowlist.
 * Max 4096 entries covers any reasonable allowlist.
 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, char[SNI_KEY_LEN]);
    __type(value, u8);
    __uint(max_entries, 4096);
} allowed_sni SEC(".maps");

/*
 * sni_events: ring buffer for SNI drop events streamed to userspace.
 * 1 MB = 1<<20. SNI drop events are rare relative to general network events.
 */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 20); /* 1 MB */
} sni_events SEC(".maps");

/* ════════════════════════════════════════════════════════════════════
 * SNI EVENT STRUCT
 * ════════════════════════════════════════════════════════════════════ */

/*
 * sni_event: emitted to sni_events ring buffer when a packet is dropped
 * due to a disallowed SNI hostname.
 */
struct sni_event {
    u64 timestamp;            /* bpf_ktime_get_ns() */
    u32 pid;                  /* tgid from bpf_get_current_pid_tgid() >> 32 */
    u32 dst_ip;               /* destination IP, network byte order */
    char hostname[SNI_KEY_LEN]; /* the disallowed SNI hostname, null-terminated */
    u8  comm[16];             /* task name from bpf_get_current_comm() */
    u8  _pad[4];              /* explicit padding for alignment */
};

/* ════════════════════════════════════════════════════════════════════
 * HELPER: lowercase a single ASCII character
 * ════════════════════════════════════════════════════════════════════ */
static __attribute__((always_inline)) u8 to_lower(u8 c) {
    if (c >= 'A' && c <= 'Z')
        return c + ('a' - 'A');
    return c;
}

/* ════════════════════════════════════════════════════════════════════
 * TC CLASSIFIER: classifier/sni_filter
 *
 * Attaches as TC egress classifier (cls_bpf) on the sandbox network
 * interface. Inspects TLS ClientHello SNI on port 443.
 *
 * Returns:
 *   TC_ACT_OK   — pass (allowed SNI, or parse failure, or non-TLS/443)
 *   TC_ACT_SHOT — drop (parseable SNI not in allowed_sni map)
 * ════════════════════════════════════════════════════════════════════ */
SEC("classifier/sni_filter")
int sni_filter(struct __sk_buff *skb) {
    void *data     = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;

    /* ── 1. Ethernet header ──────────────────────────────────────── */
    /* cls_bpf on TC includes the Ethernet header. */
    if (data + ETH_HDR_LEN > data_end)
        return TC_ACT_OK;

    /* Check EtherType == IPv4 */
    u16 eth_proto = ((u8 *)data)[12] << 8 | ((u8 *)data)[13];
    if (eth_proto != ETH_P_IP)
        return TC_ACT_OK;

    /* ── 2. IP header ────────────────────────────────────────────── */
    u8 *ip_hdr = (u8 *)data + ETH_HDR_LEN;
    if ((void *)(ip_hdr + 20) > data_end)
        return TC_ACT_OK;

    /* IP protocol must be TCP (6) */
    u8 ip_proto = ip_hdr[9];
    if (ip_proto != 6)
        return TC_ACT_OK;

    /* IP header length in bytes = (ihl & 0xf) * 4 */
    u8 ip_hdr_len = (ip_hdr[0] & 0xf) * 4;
    if (ip_hdr_len < 20)
        return TC_ACT_OK;

    /* Destination IP (for the event struct) */
    u32 dst_ip = (ip_hdr[16] << 24 | ip_hdr[17] << 16 | ip_hdr[18] << 8 | ip_hdr[19]);

    /* ── 3. TCP header ───────────────────────────────────────────── */
    u8 *tcp_hdr = ip_hdr + ip_hdr_len;
    if ((void *)(tcp_hdr + 20) > data_end)
        return TC_ACT_OK;

    /* Destination port (network byte order -> host byte order) */
    u16 dst_port = tcp_hdr[2] << 8 | tcp_hdr[3];
    if (dst_port != PORT_HTTPS)
        return TC_ACT_OK;

    /* TCP header length in bytes = (data_offset >> 4) * 4 */
    u8 tcp_hdr_len = (tcp_hdr[12] >> 4) * 4;
    if (tcp_hdr_len < 20)
        return TC_ACT_OK;

    /* ── 4. TCP payload ──────────────────────────────────────────── */
    u32 payload_off = ETH_HDR_LEN + ip_hdr_len + tcp_hdr_len;

    /* Ensure we have at least the TLS record header (5 bytes) */
    if (payload_off + 5 > (u32)(skb->len))
        return TC_ACT_OK;

    /* Use bpf_skb_load_bytes for safe bounded reads from packet data.
     * This avoids direct pointer arithmetic beyond data_end checks for
     * variable-offset reads that the verifier may reject. */

    /* ── 5. TLS record header check ──────────────────────────────── */
    /*
     * TLS record layout:
     *   [0]     ContentType  (1 byte)  — must be 22 (Handshake)
     *   [1..2]  Version      (2 bytes) — 0x0301 (TLS 1.0) or 0x0303 (TLS 1.2/1.3)
     *   [3..4]  Length       (2 bytes) — record payload length
     */
    u8 tls_hdr[5];
    if (bpf_skb_load_bytes(skb, payload_off, tls_hdr, 5) < 0)
        return TC_ACT_OK;

    if (tls_hdr[0] != TLS_HANDSHAKE_CONTENT_TYPE)
        return TC_ACT_OK;

    /* Version check: 0x0301 (TLS 1.0/1.1), 0x0303 (TLS 1.2), 0x0301 used in
     * TLS 1.3 record layer for compatibility. Accept 0x0301 and 0x0303. */
    u16 tls_version = (u16)tls_hdr[1] << 8 | tls_hdr[2];
    if (tls_version != 0x0301 && tls_version != 0x0302 && tls_version != 0x0303)
        return TC_ACT_OK;

    /* ── 6. TLS Handshake header check ───────────────────────────── */
    /*
     * Handshake message layout (immediately after TLS record header):
     *   [5]     HandshakeType (1 byte)  — must be 1 (ClientHello)
     *   [6..8]  Length        (3 bytes) — handshake message length
     */
    u8 hs_hdr[4];
    if (bpf_skb_load_bytes(skb, payload_off + 5, hs_hdr, 4) < 0)
        return TC_ACT_OK;

    if (hs_hdr[0] != TLS_HANDSHAKE_CLIENT_HELLO)
        return TC_ACT_OK;

    /* ── 7. ClientHello fixed fields ─────────────────────────────── */
    /*
     * ClientHello layout after the 4-byte handshake header (offset = payload_off+9):
     *   [0..1]   client_version       (2 bytes)
     *   [2..33]  random               (32 bytes)
     *   [34]     session_id_len       (1 byte)
     *   [35+]    session_id           (session_id_len bytes, max 32)
     *   [+0..1]  cipher_suites_len    (2 bytes)
     *   [+2+]    cipher_suites        (cipher_suites_len bytes)
     *   [+0]     compression_methods_len (1 byte)
     *   [+1+]    compression_methods  (compression_methods_len bytes)
     *   [+0..1]  extensions_len       (2 bytes)
     *   [+2+]    extensions           (extensions_len bytes)
     *
     * We use bpf_skb_load_bytes at computed offsets, with MAX_SNI_PARSE_DEPTH
     * as our total budget. If we exceed it, pass through.
     */

    /* ClientHello starts 9 bytes into the TLS record
     * (5-byte record header + 4-byte handshake header) */
    u32 off = payload_off + 9;

    /* client_version (2 bytes) + random (32 bytes) = 34 bytes to skip */
    off += 34;
    if (off + 1 > (u32)(skb->len))
        return TC_ACT_OK;
    if (off > payload_off + MAX_SNI_PARSE_DEPTH)
        return TC_ACT_OK;

    /* session_id_len */
    u8 sid_len;
    if (bpf_skb_load_bytes(skb, off, &sid_len, 1) < 0)
        return TC_ACT_OK;
    off += 1 + sid_len;

    if (off + 2 > (u32)(skb->len))
        return TC_ACT_OK;
    if (off > payload_off + MAX_SNI_PARSE_DEPTH)
        return TC_ACT_OK;

    /* cipher_suites_len (2 bytes, big-endian) */
    u8 cs_len_bytes[2];
    if (bpf_skb_load_bytes(skb, off, cs_len_bytes, 2) < 0)
        return TC_ACT_OK;
    u16 cs_len = (u16)cs_len_bytes[0] << 8 | cs_len_bytes[1];
    off += 2 + cs_len;

    if (off + 1 > (u32)(skb->len))
        return TC_ACT_OK;
    if (off > payload_off + MAX_SNI_PARSE_DEPTH)
        return TC_ACT_OK;

    /* compression_methods_len (1 byte) */
    u8 cm_len;
    if (bpf_skb_load_bytes(skb, off, &cm_len, 1) < 0)
        return TC_ACT_OK;
    off += 1 + cm_len;

    if (off + 2 > (u32)(skb->len))
        return TC_ACT_OK;
    if (off > payload_off + MAX_SNI_PARSE_DEPTH)
        return TC_ACT_OK;

    /* extensions_len (2 bytes, big-endian) */
    u8 ext_total_len_bytes[2];
    if (bpf_skb_load_bytes(skb, off, ext_total_len_bytes, 2) < 0)
        return TC_ACT_OK;
    u16 extensions_total_len = (u16)ext_total_len_bytes[0] << 8 | ext_total_len_bytes[1];
    off += 2;

    if (off > payload_off + MAX_SNI_PARSE_DEPTH)
        return TC_ACT_OK;

    /* ── 8. Extension loop: find SNI extension (type 0x0000) ──────── */
    /*
     * Each extension:
     *   [0..1] extension_type (2 bytes)
     *   [2..3] extension_len  (2 bytes)
     *   [4+]   extension_data (extension_len bytes)
     *
     * We iterate up to MAX_TLS_EXTENSIONS times to find the SNI extension.
     * Bounded iteration is required for BPF verifier compliance.
     */
    u32 ext_end = off + extensions_total_len;
    if (ext_end > (u32)(skb->len))
        ext_end = (u32)(skb->len); /* clamp to packet end */

#pragma unroll
    for (int i = 0; i < MAX_TLS_EXTENSIONS; i++) {
        if (off + 4 > ext_end)
            break; /* no more extensions */
        if (off > payload_off + MAX_SNI_PARSE_DEPTH)
            break;

        /* extension type (2 bytes) */
        u8 ext_type_bytes[2];
        if (bpf_skb_load_bytes(skb, off, ext_type_bytes, 2) < 0)
            return TC_ACT_OK;
        u16 ext_type = (u16)ext_type_bytes[0] << 8 | ext_type_bytes[1];

        /* extension length (2 bytes) */
        u8 ext_len_bytes[2];
        if (bpf_skb_load_bytes(skb, off + 2, ext_len_bytes, 2) < 0)
            return TC_ACT_OK;
        u16 ext_len = (u16)ext_len_bytes[0] << 8 | ext_len_bytes[1];

        if (ext_type != TLS_EXT_SNI) {
            /* Skip this extension */
            off += 4 + ext_len;
            continue;
        }

        /* ── 9. Found SNI extension — parse hostname ────────────── */
        /*
         * SNI extension data layout:
         *   [0..1] sni_list_length  (2 bytes)
         *   [2]    name_type        (1 byte, 0 = host_name)
         *   [3..4] name_length      (2 bytes)
         *   [5+]   hostname bytes   (name_length bytes, NOT null-terminated)
         */
        u32 sni_off = off + 4;

        if (sni_off + 5 > ext_end)
            return TC_ACT_OK;
        if (sni_off > payload_off + MAX_SNI_PARSE_DEPTH)
            return TC_ACT_OK;

        /* Skip sni_list_length (2 bytes) */
        sni_off += 2;

        /* name_type (1 byte) — must be 0 (host_name) */
        u8 name_type;
        if (bpf_skb_load_bytes(skb, sni_off, &name_type, 1) < 0)
            return TC_ACT_OK;
        if (name_type != SNI_HOSTNAME_TYPE)
            return TC_ACT_OK; /* unsupported name type, pass through */
        sni_off += 1;

        /* name_length (2 bytes) */
        u8 name_len_bytes[2];
        if (bpf_skb_load_bytes(skb, sni_off, name_len_bytes, 2) < 0)
            return TC_ACT_OK;
        u16 name_len = (u16)name_len_bytes[0] << 8 | name_len_bytes[1];
        sni_off += 2;

        if (name_len == 0 || name_len > SNI_MAX_LEN)
            return TC_ACT_OK; /* invalid length, pass through */

        if (sni_off + name_len > (u32)(skb->len))
            return TC_ACT_OK;
        if (sni_off > payload_off + MAX_SNI_PARSE_DEPTH)
            return TC_ACT_OK;

        /* ── 10. Build null-padded lowercase key ─────────────────── */
        char key[SNI_KEY_LEN];
        __builtin_memset(key, 0, SNI_KEY_LEN);

        /* Read hostname bytes into key. name_len is bounded by SNI_MAX_LEN=255
         * and SNI_KEY_LEN=256, so key[name_len] = '\0' is always valid. */
        if (bpf_skb_load_bytes(skb, sni_off, key, name_len) < 0)
            return TC_ACT_OK;

        /* Lowercase in-place. Bounded loop (max 255 chars) for verifier. */
#pragma unroll
        for (int j = 0; j < SNI_MAX_LEN; j++) {
            if (j >= name_len)
                break;
            key[j] = to_lower(key[j]);
        }

        /* ── 11. Map lookup ──────────────────────────────────────── */
        u8 *allowed = bpf_map_lookup_elem(&allowed_sni, &key);
        if (allowed) {
            /* SNI hostname is explicitly allowed — pass */
            return TC_ACT_OK;
        }

        /* SNI hostname not in allowlist — emit event and drop */
        struct sni_event *ev = bpf_ringbuf_reserve(&sni_events, sizeof(*ev), 0);
        if (ev) {
            ev->timestamp = bpf_ktime_get_ns();
            ev->pid       = (u32)(bpf_get_current_pid_tgid() >> 32);
            /* dst_ip stored in host byte order for readability in userspace */
            ev->dst_ip    = dst_ip;
            __builtin_memset(ev->hostname, 0, SNI_KEY_LEN);
            __builtin_memcpy(ev->hostname, key, name_len > SNI_MAX_LEN ? SNI_MAX_LEN : name_len);
            bpf_get_current_comm(ev->comm, sizeof(ev->comm));
            __builtin_memset(ev->_pad, 0, sizeof(ev->_pad));
            bpf_ringbuf_submit(ev, 0);
        }

        return TC_ACT_SHOT;
    }

    /* SNI extension not found within MAX_SNI_PARSE_DEPTH or MAX_TLS_EXTENSIONS —
     * best-effort: pass through to avoid blocking legitimate traffic. */
    return TC_ACT_OK;
}

char _license[] SEC("license") = "GPL";
