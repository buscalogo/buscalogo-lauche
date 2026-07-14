package store

import "fmt"

func eventKey(domain string, nonce uint64) string {
	return fmt.Sprintf("events/%s/%020d", domain, nonce)
}

func stateKey(domain string) string {
	return "state/" + domain
}

func dnsKey(domain string) string {
	return "dns/" + domain
}

func hashKey(hashHex string) string {
	return "meta/hash/" + hashHex
}

func rejectedKey(hashHex string) string {
	return "meta/rejected/" + hashHex
}
