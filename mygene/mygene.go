// Package mygene is the library behind the mygene command line:
// the HTTP client, request shaping, and the typed data models for MyGene.info.
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite, and retries the
// transient failures (429 and 5xx) that any public API throws under load.
// Build your endpoint calls and JSON decoding on top of it.
package mygene

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultUserAgent identifies the client to MyGene.info. A real, honest
// User-Agent is both polite and the thing most likely to keep you unblocked.
const DefaultUserAgent = "mygene/dev (+https://github.com/tamnd/mygene-cli)"

// Host is the site this client talks to, and the host the URI driver in
// domain.go claims.
const Host = "mygene.info"

// BaseURL is the root every request is built from.
const BaseURL = "https://" + Host + "/v3"

// defaultFields lists the annotation fields requested for every gene query.
const defaultFields = "symbol,name,taxid,entrezgene,ensembl.gene,type_of_gene,genomic_pos"

// Wire types — match the MyGene.info JSON shapes exactly.

type wireGene struct {
	ID         string      `json:"_id"`
	Score      float64     `json:"_score"`
	EntrezGene interface{} `json:"entrezgene"` // can be int or string
	Symbol     string      `json:"symbol"`
	Name       string      `json:"name"`
	TaxID      int         `json:"taxid"`
	TypeOfGene string      `json:"type_of_gene"`
	Ensembl    *struct {
		Gene string `json:"gene"`
	} `json:"ensembl"`
	GenomicPos *struct {
		Chr    string `json:"chr"`
		Start  int    `json:"start"`
		End    int    `json:"end"`
		Strand int    `json:"strand"`
	} `json:"genomic_pos"`
}

type wireSearchResp struct {
	Total int        `json:"total"`
	Hits  []wireGene `json:"hits"`
}

// Gene is the public record type: one gene from MyGene.info.
type Gene struct {
	ID          string `json:"id"            kit:"id"`
	Symbol      string `json:"symbol"`
	Name        string `json:"name,omitempty"`
	TaxID       int    `json:"tax_id,omitempty"`
	EntrezGene  string `json:"entrezgene,omitempty"`
	EnsemblGene string `json:"ensembl_gene,omitempty"`
	GeneType    string `json:"gene_type,omitempty"`
	Chromosome  string `json:"chromosome,omitempty"`
	Start       int    `json:"start,omitempty"`
	End         int    `json:"end,omitempty"`
}

func geneFromWire(w wireGene) *Gene {
	g := &Gene{
		ID:       w.ID,
		Symbol:   w.Symbol,
		Name:     w.Name,
		TaxID:    w.TaxID,
		GeneType: w.TypeOfGene,
	}
	if w.EntrezGene != nil {
		g.EntrezGene = fmt.Sprint(w.EntrezGene)
	}
	if w.Ensembl != nil {
		g.EnsemblGene = w.Ensembl.Gene
	}
	if w.GenomicPos != nil {
		g.Chromosome = w.GenomicPos.Chr
		g.Start = w.GenomicPos.Start
		g.End = w.GenomicPos.End
	}
	return g
}

// Client talks to MyGene.info over HTTP.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 30s timeout, a 300ms
// minimum gap between requests, and three retries on transient errors.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      300 * time.Millisecond,
		Retries:   3,
	}
}

// SearchGenes queries /v3/query and returns matching genes plus the total hit
// count. species may be empty (all species) or a taxid / name like "human".
func (c *Client) SearchGenes(ctx context.Context, query string, limit int, species string) ([]*Gene, int, error) {
	if limit <= 0 {
		limit = 10
	}
	u := BaseURL + "/query?q=" + url.QueryEscape(query) +
		"&size=" + fmt.Sprint(limit) +
		"&fields=" + url.QueryEscape(defaultFields)
	if species != "" {
		u += "&species=" + url.QueryEscape(species)
	}

	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, 0, err
	}
	return parseSearchResp(body)
}

// GetGene fetches a single gene by Entrez Gene ID or Ensembl ID.
func (c *Client) GetGene(ctx context.Context, geneID string) (*Gene, error) {
	u := BaseURL + "/gene/" + url.PathEscape(geneID) +
		"?fields=" + url.QueryEscape(defaultFields)

	body, err := c.Get(ctx, u)
	if err != nil {
		return nil, err
	}
	return parseGeneResp(body)
}

// Post performs an HTTP POST with a JSON body and returns the response bytes.
func (c *Client) Post(ctx context.Context, rawURL string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.doPost(ctx, rawURL, b)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("post %s: %w", rawURL, lastErr)
}

func (c *Client) doPost(ctx context.Context, rawURL string, payload []byte) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// Get fetches url and returns the response body. It paces and retries according
// to the client's settings. The caller owns nothing extra; the body is read
// fully and closed here.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// isNumeric reports whether s looks like a pure integer (Entrez Gene ID).
func isNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseSearchResp decodes a raw /query JSON response body into genes + total.
func parseSearchResp(body []byte) ([]*Gene, int, error) {
	var resp wireSearchResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("decode search response: %w", err)
	}
	genes := make([]*Gene, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		genes = append(genes, geneFromWire(h))
	}
	return genes, resp.Total, nil
}

// parseGeneResp decodes a raw /gene/<id> JSON response body into a Gene.
func parseGeneResp(body []byte) (*Gene, error) {
	var w wireGene
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, fmt.Errorf("decode gene response: %w", err)
	}
	return geneFromWire(w), nil
}

// urlEncode is a thin wrapper kept local so tests can import it without
// pulling in net/url separately.
func urlEncode(s string) string { return url.QueryEscape(s) }

// intStr converts an int to its decimal string representation.
func intStr(n int) string { return fmt.Sprint(n) }
