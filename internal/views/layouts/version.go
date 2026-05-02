package layouts

// AssetVersion é o sufixo (?v=...) anexado às URLs de /static/* para invalidar
// caches de browser a cada deploy. Setado uma vez no boot por main.go a partir
// do `-X main.version=$SHA` injetado no build.
//
// Quando vazio (binário rodado sem -ldflags ou em testes), o sufixo é "dev"
// e os assets são tratados como mutáveis durante o ciclo dev.
//
// Necessário porque /static/* serve com Cache-Control: immutable, max-age=1y —
// browsers nunca recheckariam após primeiro fetch sem cache-busting na URL.
var AssetVersion = "dev"

// AssetURL devolve "/static/<path>?v=<version>". Usado pelos layouts.
func AssetURL(path string) string {
	return "/static/" + path + "?v=" + AssetVersion
}
