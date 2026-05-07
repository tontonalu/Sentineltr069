-- +goose Up
-- +goose StatementBegin

-- ──────────────── Hint paths multi-índice WAN ────────────────
-- Vendors V-SOL, Realtek, ZTE e FiberHome frequentemente expõem o uplink
-- de internet em WANConnectionDevice.2 (ou .3) — uma WANConnectionDevice
-- por VLAN, com .1 sendo bridge mode placeholder. Os hint_paths_tr098
-- originais (00010 e 00011) só listam o índice .1, então o AutoMap do
-- wizard não encontrava match e o operador completava a homologação sem
-- as chaves WAN/PPPoE.
--
-- Adicionamos hints para índices 1..4 em todos os canonical_keys que
-- atravessam WANConnectionDevice. AutoMap testa cada hint na ordem; o
-- primeiro path que existir no snapshot do device é usado.

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.Username',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.Username',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.Username'
] WHERE key = 'pppoe.username';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.Password',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.Password',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.Password'
] WHERE key = 'pppoe.password';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.ExternalIPAddress'
] WHERE key = 'wan.ip';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.ConnectionType'
] WHERE key = 'wan.connection_type';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.DNSServers'
] WHERE key = 'wan.dns.primary';

-- wan.dns.secondary apontava para WANIPConnection no seed original; mantemos
-- esse caminho como prioritário e adicionamos as variantes de índice.
UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.DNSServers',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.DNSServers'
] WHERE key = 'wan.dns.secondary';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MaxMRUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.MaxMRUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.MaxMRUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.MaxMRUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MaxMTUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.MaxMTUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.MaxMTUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.MaxMTUSize'
] WHERE key = 'wan.mtu';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANPPPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANPPPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANPPPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.WANIPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.WANIPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.WANIPConnection.1.MACAddress'
] WHERE key = 'wan.mac';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLAN',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.X_VLAN',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.X_VLAN',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.X_VLAN',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLANIDMark',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.2.X_VLANIDMark',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.3.X_VLANIDMark',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.4.X_VLANIDMark'
] WHERE key = 'wan.vlan_id';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverte para os hints originais (mais restritos — só índice 1).
UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username'
] WHERE key = 'pppoe.username';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password'
] WHERE key = 'pppoe.password';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress'
] WHERE key = 'wan.ip';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionType'
] WHERE key = 'wan.connection_type';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers'
] WHERE key = 'wan.dns.primary';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.DNSServers'
] WHERE key = 'wan.dns.secondary';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MaxMRUSize',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MaxMTUSize'
] WHERE key = 'wan.mtu';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MACAddress',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MACAddress'
] WHERE key = 'wan.mac';

UPDATE canonical_keys SET hint_paths_tr098 = ARRAY[
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLAN',
    'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLANIDMark'
] WHERE key = 'wan.vlan_id';

-- +goose StatementEnd
