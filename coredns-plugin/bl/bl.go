// Package bl is a sketch of a CoreDNS plugin that reads the BuscaLogo registry.
//
// Phase 1 of the Agent uses CoreDNS hosts(data/registry/hosts) instead of
// compiling a custom CoreDNS binary. Phase 2 can vendor this plugin into a
// forked CoreDNS build.
//
// Lookup path when compiled in:
//
//	query exemplo.bl → GET http://127.0.0.1:9970/api/dns-query?name=exemplo.bl&type=AAAA
//	or open Badger/SQLite in read-only mode via the shared Store interface.
//
// Until then, DoH JSON is available on the Agent at:
//
//	GET /dns-query?name=exemplo.bl&type=AAAA
//	GET /api/dns-query?name=exemplo.bl&type=AAAA
package bl

// Placeholder keeps the module path reserved for the future plugin.
const Name = "bl"
