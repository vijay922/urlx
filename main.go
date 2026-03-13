package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	parser "github.com/Cgboal/DomainParser"
)

const (
	version   = "2.0.0"
	toolName  = "urlx"
	maxWorkers = 10
)

var extractor parser.Parser

func init() {
	extractor = parser.NewDomainParser()
}

func main() {
	var unique bool
	flag.BoolVar(&unique, "u", false, "Only output unique values")
	flag.BoolVar(&unique, "unique", false, "Only output unique values")

	var verbose bool
	flag.BoolVar(&verbose, "v", false, "Verbose mode (output URL parse errors)")
	flag.BoolVar(&verbose, "verbose", false, "Verbose mode (output URL parse errors)")

	var showVersion bool
	flag.BoolVar(&showVersion, "V", false, "Show version")
	flag.BoolVar(&showVersion, "version", false, "Show version")

	var noColor bool
	flag.BoolVar(&noColor, "nc", false, "Disable colored output")
	flag.BoolVar(&noColor, "no-color", false, "Disable colored output")

	var workers int
	flag.IntVar(&workers, "w", 1, "Number of concurrent workers")
	flag.IntVar(&workers, "workers", 1, "Number of concurrent workers")

	var filterExt string
	flag.StringVar(&filterExt, "fe", "", "Filter by file extension (comma-separated, e.g. js,php,html)")
	flag.StringVar(&filterExt, "filter-ext", "", "Filter by file extension (comma-separated)")

	var matchExt string
	flag.StringVar(&matchExt, "me", "", "Match only these file extensions (comma-separated)")
	flag.StringVar(&matchExt, "match-ext", "", "Match only these file extensions (comma-separated)")

	var filterScheme string
	flag.StringVar(&filterScheme, "fs", "", "Filter by scheme (comma-separated, e.g. http,ftp)")
	flag.StringVar(&filterScheme, "filter-scheme", "", "Filter by scheme (comma-separated)")

	var matchScheme string
	flag.StringVar(&matchScheme, "ms", "", "Match only these schemes (comma-separated, e.g. https)")
	flag.StringVar(&matchScheme, "match-scheme", "", "Match only these schemes (comma-separated)")

	var filterDomain string
	flag.StringVar(&filterDomain, "fd", "", "Filter by domain pattern (regex)")
	flag.StringVar(&filterDomain, "filter-domain", "", "Filter by domain pattern (regex)")

	var matchDomain string
	flag.StringVar(&matchDomain, "md", "", "Match only domains matching pattern (regex)")
	flag.StringVar(&matchDomain, "match-domain", "", "Match only domains matching pattern (regex)")

	var filterPath string
	flag.StringVar(&filterPath, "fp", "", "Filter by path pattern (regex)")
	flag.StringVar(&filterPath, "filter-path", "", "Filter by path pattern (regex)")

	var matchPath string
	flag.StringVar(&matchPath, "mp", "", "Match only paths matching pattern (regex)")
	flag.StringVar(&matchPath, "match-path", "", "Match only paths matching pattern (regex)")

	var filterKey string
	flag.StringVar(&filterKey, "fk", "", "Filter by query key (comma-separated)")
	flag.StringVar(&filterKey, "filter-key", "", "Filter by query key (comma-separated)")

	var matchKey string
	flag.StringVar(&matchKey, "mk", "", "Match only URLs with these query keys (comma-separated)")
	flag.StringVar(&matchKey, "match-key", "", "Match only URLs with these query keys (comma-separated)")

	var hasParams bool
	flag.BoolVar(&hasParams, "hp", false, "Only output URLs that have query parameters")
	flag.BoolVar(&hasParams, "has-params", false, "Only output URLs that have query parameters")

	var outputFile string
	flag.StringVar(&outputFile, "o", "", "Output file path")
	flag.StringVar(&outputFile, "output", "", "Output file path")

	var sortOutput bool
	flag.BoolVar(&sortOutput, "s", false, "Sort the output")
	flag.BoolVar(&sortOutput, "sort", false, "Sort the output")

	var count bool
	flag.BoolVar(&count, "c", false, "Show count of results")
	flag.BoolVar(&count, "count", false, "Show count of results")

	var delimiter string
	flag.StringVar(&delimiter, "d", "\n", "Output delimiter")
	flag.StringVar(&delimiter, "delimiter", "\n", "Output delimiter")

	var inputFile string
	flag.StringVar(&inputFile, "i", "", "Input file path (default: stdin)")
	flag.StringVar(&inputFile, "input", "", "Input file path (default: stdin)")

	flag.Parse()

	if showVersion {
		fmt.Fprintf(os.Stdout, "%s v%s\n", toolName, version)
		return
	}

	mode := flag.Arg(0)
	fmtStr := flag.Arg(1)

	if mode == "" {
		flag.Usage()
		return
	}

	procFn, ok := map[string]urlProc{
		"keys":       keys,
		"values":     values,
		"keypairs":   keyPairs,
		"domains":    domains,
		"domain":     domains,
		"paths":      paths,
		"path":       paths,
		"apex":       apexes,
		"apexes":     apexes,
		"json":       jsonFormat,
		"format":     format,
		"schemes":    schemes,
		"scheme":     schemes,
		"ports":      ports,
		"port":       ports,
		"extensions": extensions,
		"ext":        extensions,
		"fragments":  fragments,
		"fragment":   fragments,
		"users":      users,
		"user":       users,
		"dirs":       dirs,
		"dir":        dirs,
		"filenames":  filenames,
		"filename":   filenames,
		"subdomains": subdomains,
		"subdomain":  subdomains,
		"tlds":       tlds,
		"tld":        tlds,
		"roots":      roots,
		"root":       roots,
		"decode":     decodeURL,
		"encode":     encodeURL,
		"normalize":  normalizeURL,
		"rebuild":    rebuildURL,
		"strip":      strip,
	}[mode]

	if !ok {
		fmt.Fprintf(os.Stderr, "%s: unknown mode: %s\n", toolName, mode)
		flag.Usage()
		return
	}

	// Build filter configuration
	filterCfg := buildFilterConfig(
		filterExt, matchExt,
		filterScheme, matchScheme,
		filterDomain, matchDomain,
		filterPath, matchPath,
		filterKey, matchKey,
		hasParams, verbose,
	)

	// Determine input source
	var scanner *bufio.Scanner
	if inputFile != "" {
		f, err := os.Open(inputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: failed to open input file: %s\n", toolName, err)
			os.Exit(1)
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)
	} else {
		scanner = bufio.NewScanner(os.Stdin)
	}

	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	// Determine output destination
	var writer *bufio.Writer
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: failed to create output file: %s\n", toolName, err)
			os.Exit(1)
		}
		defer f.Close()
		writer = bufio.NewWriter(f)
		defer writer.Flush()
	} else {
		writer = bufio.NewWriter(os.Stdout)
		defer writer.Flush()
	}

	seen := make(map[string]bool)
	var results []string
	var resultCount int

	if workers > 1 && workers <= maxWorkers {
		results, resultCount = processParallel(scanner, procFn, fmtStr, filterCfg, unique, verbose, workers, seen)
	} else {
		results, resultCount = processSequential(scanner, procFn, fmtStr, filterCfg, unique, verbose, seen)
	}

	if sortOutput {
		sort.Strings(results)
	}

	for i, val := range results {
		if i > 0 && delimiter != "\n" {
			fmt.Fprint(writer, delimiter)
		}
		if delimiter == "\n" {
			fmt.Fprintln(writer, val)
		} else {
			fmt.Fprint(writer, val)
		}
	}

	if delimiter != "\n" && len(results) > 0 {
		fmt.Fprintln(writer)
	}

	if count {
		fmt.Fprintf(os.Stderr, "\n[%s] Total results: %d\n", toolName, resultCount)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: failed to read input: %s\n", toolName, err)
	}
}

// Filter configuration
type filterConfig struct {
	filterExts    map[string]bool
	matchExts     map[string]bool
	filterSchemes map[string]bool
	matchSchemes  map[string]bool
	filterDomain  *regexp.Regexp
	matchDomain   *regexp.Regexp
	filterPath    *regexp.Regexp
	matchPath     *regexp.Regexp
	filterKeys    map[string]bool
	matchKeys     map[string]bool
	hasParams     bool
}

func buildFilterConfig(
	filterExt, matchExt,
	filterScheme, matchScheme,
	filterDomain, matchDomain,
	filterPath, matchPath,
	filterKey, matchKey string,
	hasParams, verbose bool,
) filterConfig {
	cfg := filterConfig{
		hasParams: hasParams,
	}

	if filterExt != "" {
		cfg.filterExts = toSet(filterExt)
	}
	if matchExt != "" {
		cfg.matchExts = toSet(matchExt)
	}
	if filterScheme != "" {
		cfg.filterSchemes = toSet(filterScheme)
	}
	if matchScheme != "" {
		cfg.matchSchemes = toSet(matchScheme)
	}
	if filterDomain != "" {
		re, err := regexp.Compile(filterDomain)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: invalid filter-domain regex: %s\n", toolName, err)
			}
		} else {
			cfg.filterDomain = re
		}
	}
	if matchDomain != "" {
		re, err := regexp.Compile(matchDomain)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: invalid match-domain regex: %s\n", toolName, err)
			}
		} else {
			cfg.matchDomain = re
		}
	}
	if filterPath != "" {
		re, err := regexp.Compile(filterPath)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: invalid filter-path regex: %s\n", toolName, err)
			}
		} else {
			cfg.filterPath = re
		}
	}
	if matchPath != "" {
		re, err := regexp.Compile(matchPath)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: invalid match-path regex: %s\n", toolName, err)
			}
		} else {
			cfg.matchPath = re
		}
	}
	if filterKey != "" {
		cfg.filterKeys = toSet(filterKey)
	}
	if matchKey != "" {
		cfg.matchKeys = toSet(matchKey)
	}

	return cfg
}

func toSet(commaSeparated string) map[string]bool {
	set := make(map[string]bool)
	for _, item := range strings.Split(commaSeparated, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			set[strings.ToLower(item)] = true
		}
	}
	return set
}

func shouldProcess(u *url.URL, cfg filterConfig) bool {
	// Check has-params filter
	if cfg.hasParams && len(u.Query()) == 0 {
		return false
	}

	// Check scheme filters
	scheme := strings.ToLower(u.Scheme)
	if cfg.filterSchemes != nil && cfg.filterSchemes[scheme] {
		return false
	}
	if cfg.matchSchemes != nil && !cfg.matchSchemes[scheme] {
		return false
	}

	// Check domain filters
	hostname := u.Hostname()
	if cfg.filterDomain != nil && cfg.filterDomain.MatchString(hostname) {
		return false
	}
	if cfg.matchDomain != nil && !cfg.matchDomain.MatchString(hostname) {
		return false
	}

	// Check path filters
	path := u.EscapedPath()
	if cfg.filterPath != nil && cfg.filterPath.MatchString(path) {
		return false
	}
	if cfg.matchPath != nil && !cfg.matchPath.MatchString(path) {
		return false
	}

	// Check extension filters
	ext := strings.ToLower(getFileExtension(path))
	if cfg.filterExts != nil && ext != "" && cfg.filterExts[ext] {
		return false
	}
	if cfg.matchExts != nil {
		if ext == "" || !cfg.matchExts[ext] {
			return false
		}
	}

	// Check query key filters
	if cfg.filterKeys != nil || cfg.matchKeys != nil {
		queryKeys := make(map[string]bool)
		for key := range u.Query() {
			queryKeys[strings.ToLower(key)] = true
		}

		if cfg.filterKeys != nil {
			for key := range cfg.filterKeys {
				if queryKeys[key] {
					return false
				}
			}
		}

		if cfg.matchKeys != nil {
			hasMatch := false
			for key := range cfg.matchKeys {
				if queryKeys[key] {
					hasMatch = true
					break
				}
			}
			if !hasMatch {
				return false
			}
		}
	}

	return true
}

func getFileExtension(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	filename := parts[len(parts)-1]
	dotParts := strings.Split(filename, ".")
	if len(dotParts) > 1 {
		return dotParts[len(dotParts)-1]
	}
	return ""
}

func processSequential(
	scanner *bufio.Scanner,
	procFn urlProc,
	fmtStr string,
	cfg filterConfig,
	unique, verbose bool,
	seen map[string]bool,
) ([]string, int) {
	var results []string
	resultCount := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		u, err := parseURL(line)
		if err != nil {
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: parse failure: %s\n", toolName, err)
			}
			continue
		}

		if !shouldProcess(u, cfg) {
			continue
		}

		for _, val := range procFn(u, fmtStr) {
			if val == "" {
				continue
			}
			if seen[val] && unique {
				continue
			}
			results = append(results, val)
			resultCount++
			if unique {
				seen[val] = true
			}
		}
	}

	return results, resultCount
}

type processResult struct {
	values []string
}

func processParallel(
	scanner *bufio.Scanner,
	procFn urlProc,
	fmtStr string,
	cfg filterConfig,
	unique, verbose bool,
	workers int,
	seen map[string]bool,
) ([]string, int) {
	inputCh := make(chan string, workers*10)
	outputCh := make(chan processResult, workers*10)

	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for line := range inputCh {
				u, err := parseURL(line)
				if err != nil {
					if verbose {
						fmt.Fprintf(os.Stderr, "%s: parse failure: %s\n", toolName, err)
					}
					continue
				}

				if !shouldProcess(u, cfg) {
					continue
				}

				vals := procFn(u, fmtStr)
				if len(vals) > 0 {
					outputCh <- processResult{values: vals}
				}
			}
		}()
	}

	// Close output channel when all workers are done
	go func() {
		wg.Wait()
		close(outputCh)
	}()

	// Feed input
	go func() {
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				inputCh <- line
			}
		}
		close(inputCh)
	}()

	// Collect results
	var results []string
	resultCount := 0
	for res := range outputCh {
		for _, val := range res.values {
			if val == "" {
				continue
			}
			if seen[val] && unique {
				continue
			}
			results = append(results, val)
			resultCount++
			if unique {
				seen[val] = true
			}
		}
	}

	return results, resultCount
}

// parseURL parses a string as a URL and returns a *url.URL
// or any error that occurred. If the initially parsed URL
// has no scheme, http:// is prepended and the string is
// re-parsed
func parseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty URL")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	if u.Scheme == "" {
		return url.Parse("http://" + raw)
	}

	return u, nil
}

// a urlProc is any function that accepts a URL and some
// kind of format string (which may not actually be used
// by some functions), and returns a slice of strings
// derived from that URL.
type urlProc func(*url.URL, string) []string

type UrlStruct struct {
	Scheme        string     `json:"scheme"`
	Opaque        string     `json:"opaque,omitempty"`
	User          string     `json:"user,omitempty"`
	Host          string     `json:"host"`
	Path          string     `json:"path"`
	RawPath       string     `json:"raw_path,omitempty"`
	RawQuery      string     `json:"raw_query,omitempty"`
	Fragment      string     `json:"fragment,omitempty"`
	Parameters    []KeyValue `json:"parameters,omitempty"`
	Url           string     `json:"url"`
	Domain        string     `json:"domain"`
	Subdomain     string     `json:"subdomain,omitempty"`
	Root          string     `json:"root"`
	TLD           string     `json:"tld"`
	Apex          string     `json:"apex"`
	Port          string     `json:"port,omitempty"`
	PathExtension string     `json:"extension,omitempty"`
	Filename      string     `json:"filename,omitempty"`
	Directory     string     `json:"directory,omitempty"`
	IsIP          bool       `json:"is_ip"`
	Protocol      string     `json:"protocol"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func jsonFormat(u *url.URL, _ string) []string {
	parameters := make([]KeyValue, 0)
	for key, vals := range u.Query() {
		for _, val := range vals {
			parameters = append(parameters, KeyValue{Key: key, Value: val})
		}
	}

	hostname := u.Hostname()
	isIP := net.ParseIP(hostname) != nil

	apex := ""
	if !isIP {
		extractApexs := format(u, "%r.%t")
		if len(extractApexs) == 1 && extractApexs[0] != "." {
			apex = extractApexs[0]
		}
	}

	domain := hostname

	subdomain := ""
	if !isIP {
		subdomain = extractFromDomain(u, "subdomain")
	}

	root := ""
	if !isIP {
		root = extractFromDomain(u, "root")
	}

	tld := ""
	if !isIP {
		tld = extractFromDomain(u, "tld")
	}

	port := u.Port()
	extension := getFileExtension(u.EscapedPath())

	pathStr := u.EscapedPath()
	filename := filepath.Base(pathStr)
	if filename == "." || filename == "/" {
		filename = ""
	}

	directory := filepath.Dir(pathStr)
	if directory == "." {
		directory = ""
	}

	protocol := u.Scheme
	if port == "443" {
		protocol = "https"
	}

	newstructure := UrlStruct{
		Scheme:        u.Scheme,
		Opaque:        u.Opaque,
		User:          userString(u),
		Host:          u.Host,
		Path:          u.Path,
		RawPath:       u.RawPath,
		RawQuery:      u.RawQuery,
		Fragment:      u.Fragment,
		Parameters:    parameters,
		Apex:          apex,
		Url:           u.String(),
		Domain:        domain,
		Subdomain:     subdomain,
		Root:          root,
		TLD:           tld,
		Port:          port,
		PathExtension: extension,
		Filename:      filename,
		Directory:     directory,
		IsIP:          isIP,
		Protocol:      protocol,
	}

	outBytes, err := json.Marshal(newstructure)
	if err == nil {
		return []string{string(outBytes)}
	}
	return []string{""}
}

func userString(u *url.URL) string {
	if u.User != nil {
		return u.User.String()
	}
	return ""
}

// keys returns all of the keys used in the query string
func keys(u *url.URL, _ string) []string {
	out := make([]string, 0)
	for key := range u.Query() {
		out = append(out, key)
	}
	return out
}

// values returns all of the values in the query string
func values(u *url.URL, _ string) []string {
	out := make([]string, 0)
	for _, vals := range u.Query() {
		for _, val := range vals {
			out = append(out, val)
		}
	}
	return out
}

// keyPairs returns all the key=value pairs in the query string
func keyPairs(u *url.URL, _ string) []string {
	out := make([]string, 0)
	for key, vals := range u.Query() {
		for _, val := range vals {
			out = append(out, fmt.Sprintf("%s=%s", key, val))
		}
	}
	return out
}

// domains returns the domain portion of the URL
func domains(u *url.URL, _ string) []string {
	return []string{u.Hostname()}
}

// apexes returns the apex portion of the URL
func apexes(u *url.URL, _ string) []string {
	hostname := u.Hostname()
	if net.ParseIP(hostname) != nil {
		return []string{hostname}
	}
	root := extractFromDomain(u, "root")
	tld := extractFromDomain(u, "tld")
	if root == "" || tld == "" {
		return []string{hostname}
	}
	return []string{root + "." + tld}
}

// paths returns the path portion of the URL
func paths(u *url.URL, _ string) []string {
	return []string{u.EscapedPath()}
}

// schemes returns the scheme of the URL
func schemes(u *url.URL, _ string) []string {
	return []string{u.Scheme}
}

// ports returns the port of the URL
func ports(u *url.URL, _ string) []string {
	port := u.Port()
	if port == "" {
		return []string{}
	}
	return []string{port}
}

// extensions returns the file extension of the URL path
func extensions(u *url.URL, _ string) []string {
	ext := getFileExtension(u.EscapedPath())
	if ext == "" {
		return []string{}
	}
	return []string{ext}
}

// fragments returns the fragment of the URL
func fragments(u *url.URL, _ string) []string {
	if u.Fragment == "" {
		return []string{}
	}
	return []string{u.Fragment}
}

// users returns the user info of the URL
func users(u *url.URL, _ string) []string {
	if u.User == nil {
		return []string{}
	}
	return []string{u.User.String()}
}

// dirs returns the directory portion of the URL path
func dirs(u *url.URL, _ string) []string {
	path := u.EscapedPath()
	if path == "" || path == "/" {
		return []string{"/"}
	}
	dir := filepath.Dir(path)
	if dir == "." {
		return []string{"/"}
	}
	return []string{dir}
}

// filenames returns the filename portion of the URL path
func filenames(u *url.URL, _ string) []string {
	path := u.EscapedPath()
	if path == "" || path == "/" {
		return []string{}
	}
	base := filepath.Base(path)
	if base == "." || base == "/" {
		return []string{}
	}
	return []string{base}
}

// subdomains returns the subdomain portion
func subdomains(u *url.URL, _ string) []string {
	hostname := u.Hostname()
	if net.ParseIP(hostname) != nil {
		return []string{}
	}
	sub := extractFromDomain(u, "subdomain")
	if sub == "" {
		return []string{}
	}
	return []string{sub}
}

// tlds returns the TLD portion
func tlds(u *url.URL, _ string) []string {
	hostname := u.Hostname()
	if net.ParseIP(hostname) != nil {
		return []string{}
	}
	tld := extractFromDomain(u, "tld")
	if tld == "" {
		return []string{}
	}
	return []string{tld}
}

// roots returns the root domain portion
func roots(u *url.URL, _ string) []string {
	hostname := u.Hostname()
	if net.ParseIP(hostname) != nil {
		return []string{}
	}
	root := extractFromDomain(u, "root")
	if root == "" {
		return []string{}
	}
	return []string{root}
}

// decodeURL returns URL-decoded version
func decodeURL(u *url.URL, _ string) []string {
	decoded, err := url.QueryUnescape(u.String())
	if err != nil {
		return []string{u.String()}
	}
	return []string{decoded}
}

// encodeURL returns a properly encoded URL
func encodeURL(u *url.URL, _ string) []string {
	// Re-encode by rebuilding
	encoded := &url.URL{
		Scheme:   u.Scheme,
		User:     u.User,
		Host:     u.Host,
		Path:     u.Path,
		RawQuery: u.Query().Encode(),
		Fragment: u.Fragment,
	}
	return []string{encoded.String()}
}

// normalizeURL returns a normalized/canonicalized URL
func normalizeURL(u *url.URL, _ string) []string {
	// Lowercase scheme and host
	normalized := &url.URL{
		Scheme: strings.ToLower(u.Scheme),
		User:   u.User,
		Host:   strings.ToLower(u.Host),
	}

	// Normalize path
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}

	// Remove default ports
	host := normalized.Host
	if strings.ToLower(u.Scheme) == "http" && u.Port() == "80" {
		host = u.Hostname()
	} else if strings.ToLower(u.Scheme) == "https" && u.Port() == "443" {
		host = u.Hostname()
	}
	normalized.Host = host

	// Remove trailing slash unless it's the root
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	normalized.Path = path

	// Remove empty fragment
	if u.Fragment != "" {
		normalized.Fragment = u.Fragment
	}

	// Sort query parameters
	if u.RawQuery != "" {
		params := u.Query()
		normalized.RawQuery = params.Encode()
	}

	// Remove dot segments in path
	normalized.Path = filepath.Clean(normalized.Path)
	if !strings.HasPrefix(normalized.Path, "/") {
		normalized.Path = "/" + normalized.Path
	}

	return []string{normalized.String()}
}

// rebuildURL rebuilds the URL from its components
func rebuildURL(u *url.URL, fmtStr string) []string {
	if fmtStr == "" {
		// Default rebuild: scheme://domain/path?query#fragment
		return format(u, "%s://%u%@%d%:%P%p%?%q%#%f")
	}

	// Supported rebuild options
	switch strings.ToLower(fmtStr) {
	case "noparams", "no-params":
		return format(u, "%s://%u%@%d%:%P%p")
	case "nofragment", "no-fragment":
		return format(u, "%s://%u%@%d%:%P%p%?%q")
	case "base":
		return format(u, "%s://%d%:%P")
	case "pathonly", "path-only":
		return format(u, "%p%?%q")
	case "origin":
		return format(u, "%s://%d%:%P")
	default:
		return format(u, fmtStr)
	}
}

// strip removes specific parts of the URL
func strip(u *url.URL, what string) []string {
	switch strings.ToLower(what) {
	case "params", "query":
		stripped := *u
		stripped.RawQuery = ""
		return []string{stripped.String()}
	case "fragment", "hash":
		stripped := *u
		stripped.Fragment = ""
		return []string{stripped.String()}
	case "user", "userinfo":
		stripped := *u
		stripped.User = nil
		return []string{stripped.String()}
	case "port":
		stripped := *u
		stripped.Host = stripped.Hostname()
		return []string{stripped.String()}
	case "path":
		stripped := *u
		stripped.Path = ""
		stripped.RawPath = ""
		return []string{stripped.String()}
	case "scheme":
		stripped := *u
		stripped.Scheme = ""
		result := stripped.String()
		result = strings.TrimPrefix(result, "//")
		return []string{result}
	case "all":
		// Strip everything except scheme + domain
		stripped := &url.URL{
			Scheme: u.Scheme,
			Host:   u.Hostname(),
		}
		return []string{stripped.String()}
	default:
		return []string{u.String()}
	}
}

// format is a special sprintf for URLs
func format(u *url.URL, f string) []string {
	out := &bytes.Buffer{}

	inFormat := false
	for _, r := range f {

		if r == '%' && !inFormat {
			inFormat = true
			continue
		}

		if !inFormat {
			out.WriteRune(r)
			continue
		}

		switch r {

		// a literal percent rune
		case '%':
			out.WriteRune('%')

		// the scheme; e.g. http
		case 's':
			out.WriteString(u.Scheme)

		// the userinfo; e.g. user:pass
		case 'u':
			if u.User != nil {
				out.WriteString(u.User.String())
			}

		// the domain; e.g. sub.example.com
		case 'd':
			out.WriteString(u.Hostname())

		// the port; e.g. 8080
		case 'P':
			out.WriteString(u.Port())

		// the subdomain; e.g. www
		case 'S':
			out.WriteString(extractFromDomain(u, "subdomain"))

		// the root; e.g. example
		case 'r':
			out.WriteString(extractFromDomain(u, "root"))

		// the tld; e.g. com
		case 't':
			out.WriteString(extractFromDomain(u, "tld"))

		// the path; e.g. /users
		case 'p':
			out.WriteString(u.EscapedPath())

		// the path's file extension
		case 'e':
			ext := getFileExtension(u.EscapedPath())
			out.WriteString(ext)

		// the filename
		case 'F':
			path := u.EscapedPath()
			if path != "" && path != "/" {
				base := filepath.Base(path)
				if base != "." && base != "/" {
					out.WriteString(base)
				}
			}

		// the directory
		case 'D':
			path := u.EscapedPath()
			if path != "" && path != "/" {
				dir := filepath.Dir(path)
				if dir != "." {
					out.WriteString(dir)
				}
			}

		// the query string; e.g. one=1&two=2
		case 'q':
			out.WriteString(u.RawQuery)

		// the fragment / hash value; e.g. section-1
		case 'f':
			out.WriteString(u.Fragment)

		// an @ if user info is specified
		case '@':
			if u.User != nil {
				out.WriteRune('@')
			}

		// a colon if a port is specified
		case ':':
			if u.Port() != "" {
				out.WriteRune(':')
			}

		// a question mark if there's a query string
		case '?':
			if u.RawQuery != "" {
				out.WriteRune('?')
			}

		// a hash if there is a fragment
		case '#':
			if u.Fragment != "" {
				out.WriteRune('#')
			}

		// the authority; e.g. user:pass@example.com:8080
		case 'a':
			out.WriteString(format(u, "%u%@%d%:%P")[0])

		// default to literal
		default:
			out.WriteRune('%')
			out.WriteRune(r)
		}

		inFormat = false
	}

	return []string{out.String()}
}

func extractFromDomain(u *url.URL, selection string) string {
	portRe := regexp.MustCompile(`(?m):\d+$`)
	domain := portRe.ReplaceAllString(u.Host, "")

	// Skip extraction for IP addresses
	if net.ParseIP(domain) != nil {
		return ""
	}

	switch selection {
	case "subdomain":
		return extractor.GetSubdomain(domain)
	case "root":
		return extractor.GetDomain(domain)
	case "tld":
		return extractor.GetTld(domain)
	default:
		return ""
	}
}

func init() {
	flag.Usage = func() {
		h := fmt.Sprintf(`
%s - Advanced URL Parser & Formatter v%s

Usage:
  %s [OPTIONS] [MODE] [FORMATSTRING]

Options:
  -u, --unique          Only output unique values
  -v, --verbose         Verbose mode (output URL parse errors)
  -V, --version         Show version information
  -w, --workers N       Number of concurrent workers (default: 1, max: %d)
  -o, --output FILE     Output to file instead of stdout
  -i, --input FILE      Read from file instead of stdin
  -s, --sort            Sort the output
  -c, --count           Show count of results
  -d, --delimiter STR   Output delimiter (default: newline)
  -nc, --no-color       Disable colored output

Filtering Options:
  -fe, --filter-ext     Filter OUT these extensions (comma-separated)
  -me, --match-ext      Match ONLY these extensions (comma-separated)
  -fs, --filter-scheme  Filter OUT these schemes (comma-separated)
  -ms, --match-scheme   Match ONLY these schemes (comma-separated)
  -fd, --filter-domain  Filter OUT domains matching regex
  -md, --match-domain   Match ONLY domains matching regex
  -fp, --filter-path    Filter OUT paths matching regex
  -mp, --match-path     Match ONLY paths matching regex
  -fk, --filter-key     Filter OUT URLs with these query keys (comma-separated)
  -mk, --match-key      Match ONLY URLs with these query keys (comma-separated)
  -hp, --has-params     Only output URLs that have query parameters

Modes:
  keys          Keys from the query string (one per line)
  values        Values from the query string (one per line)
  keypairs      Key=value pairs from the query string (one per line)
  domains       The hostname (e.g. sub.example.com)
  paths         The request path (e.g. /users)
  apexes        The apex domain (e.g. example.com from sub.example.com)
  schemes       The URL scheme (e.g. https)
  ports         The port number (e.g. 8080)
  extensions    The file extension (e.g. js, php)
  fragments     The URL fragment (e.g. section-1)
  users         The user info (e.g. user:pass)
  dirs          The directory portion of the path
  filenames     The filename portion of the path
  subdomains    The subdomain (e.g. www, api)
  tlds          The TLD (e.g. com, co.uk)
  roots         The root domain (e.g. example)
  json          JSON encoded URL objects with full breakdown
  decode        URL-decoded version of the URL
  encode        Re-encoded/normalized URL
  normalize     Canonicalized URL (sorted params, default ports removed, etc.)
  rebuild       Rebuild URL with options (no-params, no-fragment, base, path-only, origin)
  strip         Strip URL component (params, fragment, user, port, path, scheme, all)
  format        Specify a custom format (see below)

Format Directives:
  %%%%  A literal percent character
  %%s  The request scheme (e.g. https)
  %%u  The user info (e.g. user:pass)
  %%d  The domain (e.g. sub.example.com)
  %%S  The subdomain (e.g. sub)
  %%r  The root of domain (e.g. example)
  %%t  The TLD (e.g. com)
  %%P  The port (e.g. 8080)
  %%p  The path (e.g. /users)
  %%e  The path's file extension (e.g. jpg, html)
  %%F  The filename (e.g. index.html)
  %%D  The directory (e.g. /path/to)
  %%q  The raw query string (e.g. a=1&b=2)
  %%f  The page fragment (e.g. page-section)
  %%@  Inserts an @ if user info is specified
  %%:  Inserts a colon if a port is specified
  %%?  Inserts a question mark if a query string exists
  %%#  Inserts a hash if a fragment exists
  %%a  Authority (alias for %%u%%@%%d%%:%%P)

Examples:
  cat urls.txt | %s keys
  cat urls.txt | %s -u domains
  cat urls.txt | %s format %%s://%%d%%p
  cat urls.txt | %s json
  cat urls.txt | %s -me js,php paths
  cat urls.txt | %s -hp keypairs
  cat urls.txt | %s -md "\.gov$" domains
  cat urls.txt | %s normalize
  cat urls.txt | %s strip params
  cat urls.txt | %s rebuild no-params
  cat urls.txt | %s -u -s apexes
  echo "https://user:pass@sub.example.com:8080/path/file.js?a=1&b=2#sec" | %s json
`, toolName, version, toolName, maxWorkers,
			toolName, toolName, toolName, toolName, toolName,
			toolName, toolName, toolName, toolName, toolName,
			toolName, toolName, toolName)

		fmt.Fprint(os.Stderr, h)
	}
}
