package mygene

import (
	"context"
	"fmt"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes mygene as a kit Domain: a driver that a multi-domain
// host (ant) enables with a single blank import,
//
//	import _ "github.com/tamnd/mygene-cli/mygene"
//
// exactly as a database/sql program enables a driver with `import _
// "github.com/lib/pq"`. The init below registers it; the host then dereferences
// mygene:// URIs by routing to the operations Register installs. The same
// Domain also builds the standalone mygene binary (see cli.NewApp), so the
// binary and a host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the mygene driver. It carries no state; the per-run client is
// built by the factory Register hands kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against, and
// the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "mygene",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "mygene",
			Short:  "A command line for MyGene.info gene annotations.",
			Long: `A command line for MyGene.info.

mygene reads gene annotations from MyGene.info, a BioThings API aggregating
68M+ genes across all species. Search by symbol, name, or description; fetch
individual gene records; look up exact symbols. No API key required.`,
			Site: Host,
			Repo: "https://github.com/tamnd/mygene-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{Name: "search", Group: "read", List: true,
		Summary: "Search genes by symbol, name, or description",
		Args:    []kit.Arg{{Name: "query", Help: "search query (e.g. TP53, tumor protein p53)"}}}, searchGenes)

	kit.Handle(app, kit.OpMeta{Name: "gene", Group: "read", Single: true,
		Summary: "Get a single gene by Entrez Gene ID or Ensembl ID", URIType: "gene", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "Entrez Gene ID (e.g. 7157) or Ensembl ID (e.g. ENSG00000141510)"}}}, getGene)

	kit.Handle(app, kit.OpMeta{Name: "symbol", Group: "read", List: true,
		Summary: "Find genes by exact symbol",
		Args:    []kit.Arg{{Name: "symbol", Help: "gene symbol (e.g. TP53, BRCA1)"}}}, symbolGenes)
}

// newClient builds the client from the host-resolved config, so a host and the
// standalone binary pace and identify themselves the same way.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- inputs ---

type searchInput struct {
	Query   string  `kit:"arg"          help:"search query"`
	Limit   int     `kit:"flag,inherit" help:"max results"`
	Species string  `kit:"flag"         help:"filter by species (e.g. human, 9606, mouse)"`
	Client  *Client `kit:"inject"`
}

type geneInput struct {
	ID     string  `kit:"arg"    help:"Entrez Gene ID or Ensembl ID"`
	Client *Client `kit:"inject"`
}

type symbolInput struct {
	Symbol  string  `kit:"arg"          help:"gene symbol"`
	Limit   int     `kit:"flag,inherit" help:"max results"`
	Species string  `kit:"flag"         help:"filter by species (e.g. human, 9606, mouse)"`
	Client  *Client `kit:"inject"`
}

// --- handlers ---

func searchGenes(ctx context.Context, in searchInput, emit func(*Gene) error) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	genes, _, err := in.Client.SearchGenes(ctx, in.Query, limit, in.Species)
	if err != nil {
		return mapErr(err)
	}
	for _, g := range genes {
		if err := emit(g); err != nil {
			return err
		}
	}
	return nil
}

func getGene(ctx context.Context, in geneInput, emit func(*Gene) error) error {
	g, err := in.Client.GetGene(ctx, in.ID)
	if err != nil {
		return mapErr(err)
	}
	return emit(g)
}

func symbolGenes(ctx context.Context, in symbolInput, emit func(*Gene) error) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	query := fmt.Sprintf("symbol:%s", in.Symbol)
	genes, _, err := in.Client.SearchGenes(ctx, query, limit, in.Species)
	if err != nil {
		return mapErr(err)
	}
	for _, g := range genes {
		if err := emit(g); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: the URI-native string functions, pure and network-free ---

// Classify turns any accepted input into the canonical (type, id).
// Any non-empty string is accepted as a gene ID.
func (Domain) Classify(input string) (uriType, id string, err error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", "", errs.Usage("gene ID required")
	}
	return "gene", s, nil
}

// Locate is the inverse: the live https URL for a (type, id).
// Numeric IDs resolve to the NCBI Gene page; Ensembl IDs resolve to mygene.info.
func (Domain) Locate(uriType, id string) (string, error) {
	if uriType != "gene" {
		return "", errs.Usage("mygene has no resource type %q", uriType)
	}
	if isNumeric(id) {
		return "https://www.ncbi.nlm.nih.gov/gene/" + id, nil
	}
	return "https://mygene.info/gene/" + id, nil
}

// mapErr converts a library error into the kit error kind that carries the right
// exit code.
func mapErr(err error) error {
	return err
}
