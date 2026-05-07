-- +goose Up
-- +goose StatementBegin

-- ──────────────── canonical_keys: portas físicas + PON ────────────────
-- Adiciona chaves do catálogo para mapear status físico das portas
-- (LAN1..4, WAN) e medições do GPON. Usadas pela aba "Status das portas"
-- da página de device, e indiretamente pelo coletor de telemetria.
--
-- Idempotente via ON CONFLICT (key) DO NOTHING.

INSERT INTO canonical_keys (key, label_pt, description, category, suggested_data_type, default_is_secret, hint_paths_tr098, hint_paths_tr181) VALUES

    -- ──── PON (sinal óptico) ────
    ('pon.rx_dbm', 'Sinal óptico RX (dBm)', 'Potência óptica recebida do OLT', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.X_HW_GponInfo.RxPower',
              'InternetGatewayDevice.WANDevice.1.X_GponInterafceConfig.RXPower',
              'InternetGatewayDevice.X_ZTE-COM_GponLinkInfo.0.RxPower'],
        ARRAY['Device.Optical.Interface.1.LowerLayers',
              'Device.XPON.Interface.1.Stats.RXPower']),
    ('pon.tx_dbm', 'Sinal óptico TX (dBm)', 'Potência óptica transmitida para o OLT', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.X_HW_GponInfo.TxPower',
              'InternetGatewayDevice.WANDevice.1.X_GponInterafceConfig.TXPower',
              'InternetGatewayDevice.X_ZTE-COM_GponLinkInfo.0.TxPower'],
        ARRAY['Device.XPON.Interface.1.Stats.TXPower']),

    -- ──── Portas LAN/WAN — status ────
    ('port.wan.status', 'Porta WAN — status', 'Estado físico da porta WAN (Up/Down)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANEthernetInterfaceConfig.Status',
              'InternetGatewayDevice.WANDevice.1.WANEthernetInterfaceConfig.Enable'],
        ARRAY['Device.Ethernet.Interface.1.Status']),
    ('port.lan1.status', 'Porta LAN1 — status', 'Estado físico da porta LAN1 (Up/Down)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.Status'],
        ARRAY['Device.Ethernet.Interface.2.Status']),
    ('port.lan2.status', 'Porta LAN2 — status', 'Estado físico da porta LAN2 (Up/Down)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.2.Status'],
        ARRAY['Device.Ethernet.Interface.3.Status']),
    ('port.lan3.status', 'Porta LAN3 — status', 'Estado físico da porta LAN3 (Up/Down)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.3.Status'],
        ARRAY['Device.Ethernet.Interface.4.Status']),
    ('port.lan4.status', 'Porta LAN4 — status', 'Estado físico da porta LAN4 (Up/Down)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.4.Status'],
        ARRAY['Device.Ethernet.Interface.5.Status']),

    -- ──── Portas LAN — velocidade (read-only no CPE típico) ────
    ('port.lan1.speed', 'Porta LAN1 — velocidade (Mbps)', 'Velocidade de negociação da porta LAN1', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.MaxBitRate',
              'InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.X_HW_NegotiatedSpeed'],
        ARRAY['Device.Ethernet.Interface.2.MaxBitRate', 'Device.Ethernet.Interface.2.CurrentBitRate']),
    ('port.lan2.speed', 'Porta LAN2 — velocidade (Mbps)', 'Velocidade de negociação da porta LAN2', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.2.MaxBitRate'],
        ARRAY['Device.Ethernet.Interface.3.MaxBitRate', 'Device.Ethernet.Interface.3.CurrentBitRate']),
    ('port.lan3.speed', 'Porta LAN3 — velocidade (Mbps)', 'Velocidade de negociação da porta LAN3', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.3.MaxBitRate'],
        ARRAY['Device.Ethernet.Interface.4.MaxBitRate', 'Device.Ethernet.Interface.4.CurrentBitRate']),
    ('port.lan4.speed', 'Porta LAN4 — velocidade (Mbps)', 'Velocidade de negociação da porta LAN4', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.4.MaxBitRate'],
        ARRAY['Device.Ethernet.Interface.5.MaxBitRate', 'Device.Ethernet.Interface.5.CurrentBitRate']),

    -- ──── Portas LAN — duplex ────
    ('port.lan1.duplex', 'Porta LAN1 — duplex', 'Duplex negociado (Full/Half)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.1.DuplexMode'],
        ARRAY['Device.Ethernet.Interface.2.DuplexMode']),
    ('port.lan2.duplex', 'Porta LAN2 — duplex', 'Duplex negociado (Full/Half)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.2.DuplexMode'],
        ARRAY['Device.Ethernet.Interface.3.DuplexMode']),
    ('port.lan3.duplex', 'Porta LAN3 — duplex', 'Duplex negociado (Full/Half)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.3.DuplexMode'],
        ARRAY['Device.Ethernet.Interface.4.DuplexMode']),
    ('port.lan4.duplex', 'Porta LAN4 — duplex', 'Duplex negociado (Full/Half)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANEthernetInterfaceConfig.4.DuplexMode'],
        ARRAY['Device.Ethernet.Interface.5.DuplexMode'])

ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM canonical_keys WHERE key IN (
    'pon.rx_dbm', 'pon.tx_dbm',
    'port.wan.status',
    'port.lan1.status', 'port.lan2.status', 'port.lan3.status', 'port.lan4.status',
    'port.lan1.speed',  'port.lan2.speed',  'port.lan3.speed',  'port.lan4.speed',
    'port.lan1.duplex', 'port.lan2.duplex', 'port.lan3.duplex', 'port.lan4.duplex'
);

-- +goose StatementEnd
