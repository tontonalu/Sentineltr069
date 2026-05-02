// Package sentinelacs é o pacote raiz do projeto. Existe apenas para
// hospedar diretivas //go:embed que precisam ficar no topo da árvore
// (embed não atravessa ".." entre pacotes).
//
// Tudo que é embutido aqui vira parte do binário compilado — não há
// arquivos soltos (.sql, .css, .js, html) no FS do servidor de produção.
// Isso reduz superfície de exfiltração caso o servidor seja comprometido:
// o atacante levaria apenas binários strippados (-s -w -trimpath).
package sentinelacs

import "embed"

// MigrationsFS contém todos os arquivos SQL de migration.
// Usado por cmd/migrate via goose.SetBaseFS.
//
// Sem prefixo "all:" → arquivos começando com . ou _ ficam de fora
// (i.e., .gitkeep não é embutido).
//
//go:embed migrations
var MigrationsFS embed.FS

// StaticFS contém os assets web (CSS compilado do Tailwind, JS de HTMX/Alpine, ícones).
// Servido por cmd/server em /static/* via http.FileServerFS.
//
//go:embed web/static
var StaticFS embed.FS
