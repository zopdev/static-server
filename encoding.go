package main

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gofr.dev/pkg/gofr/datasource/file"
)

const (
	acceptEncodingHeader  = "Accept-Encoding"
	contentEncodingHeader = "Content-Encoding"
	contentTypeHeader     = "Content-Type"
	wildcardCoding        = "*"

	// sniffLen matches what http.ServeFile reads to sniff a type, so an
	// encoded response is labeled exactly as the plain one would be.
	sniffLen = 512
)

// precompressed is one sidecar encoding: the Content-Encoding token a client
// names and the suffix the site build appends to the original file's name.
type precompressed struct {
	coding string
	suffix string
}

// sidecarEncodings lists the encodings this server can serve, in order of
// preference. Brotli comes first and wins whenever the client accepts both,
// whatever q-values it gave them: br is smaller for the text this server
// carries, and every browser that sends `br` decodes it. q-values decide only
// whether a coding is acceptable at all, never which acceptable one is used.
//
// An array returned by value rather than a package-level slice: fixed,
// allocation-free, and not mutable from elsewhere.
func sidecarEncodings() [2]precompressed {
	return [2]precompressed{
		{coding: "br", suffix: ".br"},
		{coding: "gzip", suffix: ".gz"},
	}
}

// qvaluePattern is the RFC 9110 §12.4.2 qvalue grammar: 0 to 1 with at most
// three decimals. Anything else — `q=abc`, `q=2`, `q=0.5000`, `q=NaN` — is
// malformed, and a malformed weight is not read as consent.
var qvaluePattern = regexp.MustCompile(`^(?:0(?:\.\d{0,3})?|1(?:\.0{0,3})?)$`)

// codingVerdict is what one Accept-Encoding header said about a single coding.
type codingVerdict int

const (
	// verdictUnnamed: the header did not mention the coding.
	verdictUnnamed codingVerdict = iota
	// verdictAccepted: named with a positive weight.
	verdictAccepted
	// verdictRefused: named with q=0, or with a weight that does not parse.
	verdictRefused
)

// acceptedEncodings is a parsed Accept-Encoding header.
type acceptedEncodings struct {
	codings  map[string]codingVerdict
	wildcard codingVerdict
}

// parseAcceptEncoding parses every Accept-Encoding field line of a request.
//
// Repeated field lines are one comma-separated list (RFC 9110 §5.3), so they
// are joined before parsing. Codings are case-insensitive and whitespace
// around elements and parameters is ignored.
//
// When a coding is named more than once, a refusal wins over an acceptance:
// sending br to a client that did not ask for it is the failure that matters,
// so a contradictory header is read in the direction that cannot cause it.
func parseAcceptEncoding(values []string) acceptedEncodings {
	parsed := acceptedEncodings{codings: map[string]codingVerdict{}}

	for _, element := range strings.Split(strings.Join(values, ","), ",") {
		coding, verdict, ok := parseCodingElement(element)
		if !ok {
			continue
		}

		if coding == wildcardCoding {
			parsed.wildcard = combineVerdicts(parsed.wildcard, verdict)

			continue
		}

		parsed.codings[coding] = combineVerdicts(parsed.codings[coding], verdict)
	}

	return parsed
}

// combineVerdicts merges two statements about the same coding: refusal beats
// acceptance, and either beats silence.
func combineVerdicts(current, next codingVerdict) codingVerdict {
	if current == verdictRefused || next == verdictRefused {
		return verdictRefused
	}

	if current == verdictAccepted || next == verdictAccepted {
		return verdictAccepted
	}

	return verdictUnnamed
}

// parseCodingElement parses one `coding;q=x` element. An empty element is
// reported as not ok and skipped; a coding whose weight is malformed is
// reported as refused.
func parseCodingElement(element string) (string, codingVerdict, bool) {
	parts := strings.Split(element, ";")

	coding := strings.ToLower(strings.TrimSpace(parts[0]))
	if coding == "" {
		return "", verdictUnnamed, false
	}

	verdict := verdictAccepted

	for _, param := range parts[1:] {
		name, value, _ := strings.Cut(param, "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}

		value = strings.TrimSpace(value)
		if !qvaluePattern.MatchString(value) {
			return coding, verdictRefused, true
		}

		// The pattern guarantees this parses.
		if q, _ := strconv.ParseFloat(value, 64); q == 0 {
			verdict = verdictRefused
		}
	}

	return coding, verdict, true
}

// accepts reports whether the client is willing to receive coding. A coding
// the client named decides for itself; otherwise `*` speaks for it; otherwise
// it is not acceptable.
func (a acceptedEncodings) accepts(coding string) bool {
	if verdict, named := a.codings[coding]; named {
		return verdict == verdictAccepted
	}

	return a.wildcard == verdictAccepted
}

// sidecar is a precompressed file found next to the file a request resolved to.
type sidecar struct {
	precompressed

	path string
}

// sidecarIndex maps the path of a file (exactly as resolveFilePath builds it)
// to the precompressed variants found next to it, in preference order. It is
// built once at startup, so serving a request costs a map lookup and not a
// filesystem probe per encoding: a site that ships no sidecars pays nothing.
type sidecarIndex map[string][]precompressed

// wellKnownDir is never indexed: every /.well-known/ request is delegated
// untouched (handler.go), so a sidecar there could never be served.
const wellKnownDir = ".well-known"

// buildSidecarIndex walks root once through fs and records, for every file,
// which of its sidecars exist alongside it. An orphan `x.br` with no `x` is
// not recorded, so it can never answer `/x`. Symlinked directories are not
// followed (gofr's ReadDir reports them as files).
//
// It never fails the caller: a directory that cannot be read is skipped and
// reported in the returned error, and whatever was indexed is still returned.
// An empty index means every response is the plain file, as before.
func buildSidecarIndex(fs file.FileSystem, root string) (sidecarIndex, error) {
	index := sidecarIndex{}

	var errs []error

	pending := []string{root}

	for len(pending) > 0 {
		dir := pending[len(pending)-1]
		pending = pending[:len(pending)-1]

		entries, err := fs.ReadDir(dir)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		names := make(map[string]struct{}, len(entries))

		for _, entry := range entries {
			if entry.IsDir() {
				if entry.Name() != wellKnownDir {
					pending = append(pending, filepath.Join(dir, entry.Name()))
				}

				continue
			}

			names[entry.Name()] = struct{}{}
		}

		for name := range names {
			var found []precompressed

			for _, enc := range sidecarEncodings() {
				if _, ok := names[name+enc.suffix]; ok {
					found = append(found, enc)
				}
			}

			if len(found) > 0 {
				index[filepath.Join(dir, name)] = found
			}
		}
	}

	return index, errors.Join(errs...)
}

// size reports how many files have at least one sidecar.
func (s sidecarIndex) size() int { return len(s) }

// findSidecars reports which precompressed variants exist for filePath, in
// preference order, from the startup index. It touches no filesystem.
func (h *staticFileHandler) findSidecars(filePath string) []sidecar {
	encs := h.sidecars[filePath]
	if len(encs) == 0 {
		return nil
	}

	found := make([]sidecar, 0, len(encs))
	for _, enc := range encs {
		found = append(found, sidecar{precompressed: enc, path: filePath + enc.suffix})
	}

	return found
}

// servePrecompressed answers the request from a precompressed sidecar of
// filePath when one exists and the client accepts its encoding, and reports
// whether it did. When it returns false the caller serves filePath as before.
//
// With no sidecar on disk it touches nothing at all, so a site that ships none
// gets exactly the responses it got before this existed.
func (h *staticFileHandler) servePrecompressed(w http.ResponseWriter, r *http.Request, filePath string, modtime time.Time) bool {
	// http.ServeFile redirects `/index.html` to `./` and rejects `..` with a
	// 400. Both must stay exactly as they are, so neither is touched here.
	if strings.HasSuffix(r.URL.Path, indexHTML) || containsDotDot(r.URL.Path) {
		return false
	}

	sidecars := h.findSidecars(filePath)
	if len(sidecars) == 0 {
		return false
	}

	// The body of this URL now depends on Accept-Encoding, whichever variant
	// this particular response carries — including the plain one, or a shared
	// cache would hand it to the next client that asked for br, and vice versa.
	advertiseVaries(w.Header(), acceptEncodingHeader)

	accepted := parseAcceptEncoding(r.Header.Values(acceptEncodingHeader))

	for _, sc := range sidecars {
		if !accepted.accepts(sc.coding) {
			continue
		}

		if serveSidecar(w, r, h.staticFilePath, filePath, modtime, sc) {
			return true
		}
	}

	return false
}

// serveSidecar writes one encoded response. It reports false, having written
// nothing, when the sidecar cannot be opened, so the caller can still answer
// with the plain file.
func serveSidecar(w http.ResponseWriter, r *http.Request, root, filePath string, modtime time.Time, sc sidecar) bool {
	f, err := openInRoot(root, sc.path)
	if err != nil {
		return false
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}

	header := w.Header()

	// The type describes the representation, not the transfer: it is that of
	// the original. A `_headers` rule, or the markdown label set by the
	// caller, has already set one and wins; otherwise it is derived exactly as
	// http.ServeFile would derive it for the plain file — never from the
	// sidecar, whose bytes sniff as application/octet-stream.
	if _, set := header[contentTypeHeader]; !set {
		header.Set(contentTypeHeader, originalContentType(root, filePath))
	}

	header.Set(contentEncodingHeader, sc.coding)

	// Ranges address bytes of one encoding; serving them against the sidecar
	// would hand back a slice that cannot be decoded on its own. The whole
	// encoded body is sent instead, which RFC 9110 §14.2 permits.
	plain := r.Clone(r.Context())
	plain.Header.Del("Range")
	plain.Header.Del("If-Range")

	// Last-Modified comes from the original, so a conditional request gets
	// the same 304-or-200 decision whichever encoding it would receive.
	http.ServeContent(&encodedResponseWriter{ResponseWriter: w, size: info.Size()}, plain, filePath, modtime, f)

	return true
}

// originalContentType is the type http.ServeFile would give the plain file:
// by extension, else sniffed from the file's own leading bytes. The distroless
// image has no /etc/mime.types, so the sniff is what decides `.md` in prod.
func originalContentType(root, filePath string) string {
	if ctype := mime.TypeByExtension(filepath.Ext(filePath)); ctype != "" {
		return ctype
	}

	f, err := openInRoot(root, filePath)
	if err != nil {
		return http.DetectContentType(nil)
	}

	defer func() { _ = f.Close() }()

	buf := make([]byte, sniffLen)
	n, _ := io.ReadFull(f, buf)

	return http.DetectContentType(buf[:n])
}

// errOutsideRoot is returned for a path that does not lie under the static root.
var errOutsideRoot = errors.New("path escapes the static root")

// openInRoot opens path, which must lie under root, through os.OpenInRoot: the
// OS refuses any component (`..`, a symlink) that would leave root, so a request
// path can never reach a file outside the site, whatever resolveFilePath did.
func openInRoot(root, path string) (*os.File, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, errOutsideRoot
	}

	return os.OpenInRoot(root, rel)
}

// encodedResponseWriter adjusts what http.ServeContent writes for an encoded
// body. ServeContent deliberately omits Content-Length whenever
// Content-Encoding is set, and always advertises byte ranges; here the encoded
// length is known exactly — and HEAD must report it — while ranges are not
// offered at all.
type encodedResponseWriter struct {
	http.ResponseWriter

	size int64
}

func (w *encodedResponseWriter) WriteHeader(status int) {
	w.Header().Del("Accept-Ranges")

	if status == http.StatusOK {
		w.Header().Set("Content-Length", strconv.FormatInt(w.size, 10))
	}

	w.ResponseWriter.WriteHeader(status)
}

// ReadFrom keeps net/http's sendfile fast path, which wrapping would
// otherwise hide from the io.Copy inside ServeContent.
func (w *encodedResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}

	return io.Copy(w.ResponseWriter, src)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *encodedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// containsDotDot mirrors the check http.ServeFile applies before serving.
func containsDotDot(v string) bool {
	if !strings.Contains(v, "..") {
		return false
	}

	for _, ent := range strings.FieldsFunc(v, func(r rune) bool { return r == '/' || r == '\\' }) {
		if ent == ".." {
			return true
		}
	}

	return false
}
