package inventory

import "strings"

// chipsetVendors são fabricantes de SOC/chipset cujo nome aparece em
// `DeviceInfo.Manufacturer` mesmo quando o equipamento é vendido por outra
// marca. Em ONTs Vsol, Intelbras, FiberHome (e outras) que usam SDKs Realtek
// ou Broadcom, o stack TR-069 vem populado com o nome do chipset vendor —
// não o brand real. Quando vemos um desses, ignoramos e tentamos resolver
// pelo OUI.
//
// Lowercase para lookup case-insensitive.
var chipsetVendors = map[string]bool{
	"realtek":   true,
	"broadcom":  true,
	"mediatek":  true,
	"ralink":    true,
	"atheros":   true,
	"qualcomm":  true,
	"qualcomm atheros":  true,
	"infineon":  true,
	"lantiq":    true,
}

// ouiVendorRegistry mapeia OUI (3 bytes em hex maiúsculo, sem separadores)
// → nome do fabricante real. Curado manualmente: cobre cenários onde o
// `DeviceInfo.Manufacturer` reportado é o chipset, mas o brand é outro.
//
// Como adicionar uma entrada nova:
//  1. descubra o OUI do device (primeiros 6 caracteres do MAC, em UPPER)
//  2. valide na base oficial IEEE: https://standards-oui.ieee.org/oui/oui.txt
//  3. acrescente uma linha aqui
//
// Migrar para tabela `oui_registry` em Postgres é um TODO quando este mapa
// crescer demais para manter em código (provavelmente >50 entradas).
var ouiVendorRegistry = map[string]string{
	// V-SOL (Hisense / V-SOL Group) — ONTs/OLTs GPON populares no BR
	"B46415": "V-SOL",

	// Adicione novas entradas seguindo o padrão "OUI": "Vendor"
}

// resolveManufacturerName decide o nome do fabricante para o sync, dado o
// que o CPE reportou em DeviceInfo.Manufacturer e o OUI do equipamento.
//
// Ordem de resolução:
//  1. Se `manufacturer` é vazio ou um chipset vendor conhecido → tenta
//     OUI lookup. Se hit, devolve o nome do registry.
//  2. Caso contrário → devolve o `manufacturer` original (já é um brand
//     legítimo na visão do sync).
//
// Quando o OUI é desconhecido E o manufacturer é chipset, devolvemos o
// chipset name como fallback — é melhor ter algo que zero. Operador pode
// renomear o vendor depois via /settings/vendors/{id}/edit.
func resolveManufacturerName(manufacturer, oui string) string {
	mfr := strings.TrimSpace(manufacturer)
	useOUI := mfr == "" || isChipsetVendor(mfr)
	if useOUI {
		if name, ok := lookupOUIVendor(oui); ok {
			return name
		}
	}
	return mfr
}

func isChipsetVendor(name string) bool {
	return chipsetVendors[strings.ToLower(strings.TrimSpace(name))]
}

func lookupOUIVendor(oui string) (string, bool) {
	o := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(oui), ":", ""))
	o = strings.ReplaceAll(o, "-", "")
	if len(o) < 6 {
		return "", false
	}
	name, ok := ouiVendorRegistry[o[:6]]
	return name, ok
}
