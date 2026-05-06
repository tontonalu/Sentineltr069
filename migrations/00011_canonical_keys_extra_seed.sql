-- +goose Up
-- +goose StatementBegin

-- ──────────────── Seed extra de canonical_keys ────────────────
-- Cobre parâmetros adicionais comumente alterados em ONTs/Roteadores
-- brasileiros (V-SOL, Intelbras, Huawei, ZTE, TP-Link, FiberHome).
-- Hints incluem variações conhecidas de paths TR-098 e TR-181.
--
-- Idempotente via ON CONFLICT (key) DO NOTHING — repetir esta migration
-- não duplica nem altera entradas já cadastradas.

INSERT INTO canonical_keys (key, label_pt, description, category, suggested_data_type, default_is_secret, hint_paths_tr098, hint_paths_tr181) VALUES

    -- ──── Wi-Fi extras ────
    ('wifi.security.mode.2g', 'Modo de segurança Wi-Fi 2.4GHz', 'WPA2-PSK / WPA3-PSK / Open / Mixed', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BeaconType',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.IEEE11iAuthenticationMode'],
        ARRAY['Device.WiFi.AccessPoint.1.Security.ModeEnabled']),
    ('wifi.security.mode.5g', 'Modo de segurança Wi-Fi 5GHz', 'WPA2-PSK / WPA3-PSK / Open / Mixed', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.BeaconType',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.IEEE11iAuthenticationMode'],
        ARRAY['Device.WiFi.AccessPoint.5.Security.ModeEnabled', 'Device.WiFi.AccessPoint.2.Security.ModeEnabled']),
    ('wifi.bandwidth.2g', 'Largura de canal Wi-Fi 2.4GHz', '20MHz / 40MHz', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.X_BANDWIDTH',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.OperatingChannelBandwidth'],
        ARRAY['Device.WiFi.Radio.1.OperatingChannelBandwidth']),
    ('wifi.bandwidth.5g', 'Largura de canal Wi-Fi 5GHz', '20/40/80/160 MHz', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.X_BANDWIDTH',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.OperatingChannelBandwidth'],
        ARRAY['Device.WiFi.Radio.2.OperatingChannelBandwidth']),
    ('wifi.standard.2g', 'Padrão Wi-Fi 2.4GHz', 'b/g/n/ax', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Standard'],
        ARRAY['Device.WiFi.Radio.1.OperatingStandards']),
    ('wifi.standard.5g', 'Padrão Wi-Fi 5GHz', 'a/n/ac/ax', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.Standard'],
        ARRAY['Device.WiFi.Radio.2.OperatingStandards']),
    ('wifi.hidden.2g', 'SSID oculto 2.4GHz', 'Esconde SSID do beacon (true=oculto)', 'wifi', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSIDAdvertisementEnabled'],
        ARRAY['Device.WiFi.AccessPoint.1.SSIDAdvertisementEnabled']),
    ('wifi.hidden.5g', 'SSID oculto 5GHz', 'Esconde SSID do beacon (true=oculto)', 'wifi', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.SSIDAdvertisementEnabled'],
        ARRAY['Device.WiFi.AccessPoint.5.SSIDAdvertisementEnabled', 'Device.WiFi.AccessPoint.2.SSIDAdvertisementEnabled']),
    ('wifi.tx_power.2g', 'Potência TX Wi-Fi 2.4GHz (%)', 'Percentual de potência de transmissão (1-100)', 'wifi', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.TransmitPower'],
        ARRAY['Device.WiFi.Radio.1.TransmitPower']),
    ('wifi.tx_power.5g', 'Potência TX Wi-Fi 5GHz (%)', 'Percentual de potência de transmissão (1-100)', 'wifi', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.TransmitPower'],
        ARRAY['Device.WiFi.Radio.2.TransmitPower']),
    ('wifi.country_code', 'Código de país Wi-Fi', 'Regulatory domain (BR, US, etc)', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.RegulatoryDomain',
              'InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.X_TP_Region'],
        ARRAY['Device.WiFi.Radio.1.RegulatoryDomain']),
    ('wifi.bssid.2g', 'BSSID Wi-Fi 2.4GHz', 'MAC do AP 2.4GHz (geralmente read-only)', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BSSID'],
        ARRAY['Device.WiFi.SSID.1.BSSID']),
    ('wifi.bssid.5g', 'BSSID Wi-Fi 5GHz', 'MAC do AP 5GHz (geralmente read-only)', 'wifi', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.WLANConfiguration.5.BSSID'],
        ARRAY['Device.WiFi.SSID.5.BSSID', 'Device.WiFi.SSID.2.BSSID']),

    -- ──── WAN extras ────
    ('wan.connection_type', 'Tipo de conexão WAN', 'PPPoE / IPoE / Bridge', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType',
              'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionType'],
        ARRAY['Device.PPP.Interface.1.ConnectionType']),
    ('wan.dns.primary', 'DNS primário WAN', 'IP do DNS primário entregue/configurado na WAN', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers'],
        ARRAY['Device.DNS.Client.Server.1.DNSServer']),
    ('wan.dns.secondary', 'DNS secundário WAN', 'IP do DNS secundário (quando aplicável)', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.DNSServers'],
        ARRAY['Device.DNS.Client.Server.2.DNSServer']),
    ('wan.mtu', 'MTU WAN', 'Maximum Transmission Unit do enlace WAN (bytes)', 'wan', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MaxMRUSize',
              'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MaxMTUSize'],
        ARRAY['Device.PPP.Interface.1.MaxMRUSize', 'Device.IP.Interface.1.MaxMTUSize']),
    ('wan.mac', 'MAC WAN', 'MAC address da interface WAN (clone)', 'wan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MACAddress',
              'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.MACAddress'],
        ARRAY['Device.Ethernet.Link.1.MACAddress']),
    ('wan.vlan_id', 'VLAN ID WAN', 'Tag 802.1Q da WAN (0-4094)', 'wan', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLAN',
              'InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.X_VLANIDMark'],
        ARRAY['Device.Ethernet.VLANTermination.1.VLANID']),

    -- ──── LAN extras ────
    ('lan.ip', 'IP LAN do roteador', 'Endereço IP da interface LAN do CPE', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress'],
        ARRAY['Device.IP.Interface.2.IPv4Address.1.IPAddress', 'Device.IP.Interface.1.IPv4Address.1.IPAddress']),
    ('lan.subnet', 'Máscara LAN', 'Subnet mask da LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceSubnetMask'],
        ARRAY['Device.IP.Interface.2.IPv4Address.1.SubnetMask']),
    ('lan.dhcp.enable', 'DHCP server ativo', 'Habilita servidor DHCP na LAN', 'lan', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerEnable'],
        ARRAY['Device.DHCPv4.Server.Enable', 'Device.DHCPv4.Server.Pool.1.Enable']),
    ('lan.dhcp.lease_time', 'DHCP — tempo de lease (s)', 'Lease time do pool DHCP em segundos', 'lan', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPLeaseTime'],
        ARRAY['Device.DHCPv4.Server.Pool.1.LeaseTime']),
    ('lan.dns.primary', 'DNS primário entregue na LAN', 'Servidor DNS anunciado pelo DHCP da LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DNSServers'],
        ARRAY['Device.DHCPv4.Server.Pool.1.DNSServers']),
    ('lan.domain_name', 'Domínio LAN', 'Domain name entregue pelo DHCP da LAN', 'lan', 'string', FALSE,
        ARRAY['InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DomainName'],
        ARRAY['Device.DHCPv4.Server.Pool.1.DomainName']),

    -- ──── Mgmt / Tempo ────
    ('mgmt.hostname', 'Hostname do CPE', 'Nome de host reportado pelo CPE', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.X_HostName',
              'InternetGatewayDevice.DeviceInfo.X_TP_HostName'],
        ARRAY['Device.DeviceInfo.HostName']),
    ('mgmt.upgrades_managed', 'Upgrades gerenciados pelo ACS', 'ManagementServer.UpgradesManaged (controla quem decide upgrade)', 'mgmt', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.ManagementServer.UpgradesManaged'],
        ARRAY['Device.ManagementServer.UpgradesManaged']),
    ('time.ntp.enable', 'NTP ativo', 'Habilita sincronização NTP no CPE', 'mgmt', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.Time.Enable'],
        ARRAY['Device.Time.Enable']),
    ('time.ntp.server.primary', 'NTP — servidor primário', 'Servidor NTP primário', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Time.NTPServer1'],
        ARRAY['Device.Time.NTPServer1']),
    ('time.ntp.server.secondary', 'NTP — servidor secundário', 'Servidor NTP secundário', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Time.NTPServer2'],
        ARRAY['Device.Time.NTPServer2']),
    ('time.timezone', 'Fuso horário', 'Local time zone (POSIX TZ ou nome)', 'mgmt', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Time.LocalTimeZone',
              'InternetGatewayDevice.Time.LocalTimeZoneName'],
        ARRAY['Device.Time.LocalTimeZone', 'Device.Time.LocalTimeZoneName']),

    -- ──── Device extras ────
    ('device.hardware.version', 'Versão de hardware', 'Hardware revision reportada pelo CPE', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.HardwareVersion'],
        ARRAY['Device.DeviceInfo.HardwareVersion']),
    ('device.product_class', 'Product Class', 'ProductClass do TR-069 DeviceID', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.ProductClass'],
        ARRAY['Device.DeviceInfo.ProductClass']),
    ('device.spec_version', 'Spec Version TR-069', 'Versão do data model TR-069 implementado', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.SpecVersion'],
        ARRAY['Device.DeviceInfo.SpecVersion']),
    ('device.provisioning_code', 'Provisioning Code', 'Código de provisionamento (geralmente SP/região)', 'device', 'string', FALSE,
        ARRAY['InternetGatewayDevice.DeviceInfo.ProvisioningCode'],
        ARRAY['Device.DeviceInfo.ProvisioningCode']),

    -- ──── Voz / SIP extras ────
    ('voice.sip.proxy_port', 'Porta do proxy SIP', 'Porta UDP/TCP do proxy SIP', 'voice', 'unsignedInt', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.SIP.ProxyServerPort'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.SIP.ProxyServerPort']),
    ('voice.sip.registrar', 'Servidor de Registro SIP', 'Endereço do registrar SIP', 'voice', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.SIP.RegistrarServer'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.SIP.RegistrarServer']),
    ('voice.sip.codec_preferred', 'Codec preferido', 'Codec SIP de mais alta prioridade', 'voice', 'string', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.Line.1.Codec.List.1.Codec'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.Line.1.Codec.List.1.Codec']),
    ('voice.sip.line_enable', 'Linha SIP ativa', 'Habilita a linha SIP', 'voice', 'bool', FALSE,
        ARRAY['InternetGatewayDevice.Services.VoiceService.1.VoiceProfile.1.Line.1.Enable'],
        ARRAY['Device.Services.VoiceService.1.VoiceProfile.1.Line.1.Enable'])

ON CONFLICT (key) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Reverte apenas as chaves que esta migration adiciona — mantém o seed
-- original (00010) intacto.
DELETE FROM canonical_keys WHERE key IN (
    'wifi.security.mode.2g','wifi.security.mode.5g',
    'wifi.bandwidth.2g','wifi.bandwidth.5g',
    'wifi.standard.2g','wifi.standard.5g',
    'wifi.hidden.2g','wifi.hidden.5g',
    'wifi.tx_power.2g','wifi.tx_power.5g',
    'wifi.country_code','wifi.bssid.2g','wifi.bssid.5g',
    'wan.connection_type','wan.dns.primary','wan.dns.secondary',
    'wan.mtu','wan.mac','wan.vlan_id',
    'lan.ip','lan.subnet','lan.dhcp.enable','lan.dhcp.lease_time',
    'lan.dns.primary','lan.domain_name',
    'mgmt.hostname','mgmt.upgrades_managed',
    'time.ntp.enable','time.ntp.server.primary','time.ntp.server.secondary',
    'time.timezone',
    'device.hardware.version','device.product_class',
    'device.spec_version','device.provisioning_code',
    'voice.sip.proxy_port','voice.sip.registrar',
    'voice.sip.codec_preferred','voice.sip.line_enable'
);

-- +goose StatementEnd
