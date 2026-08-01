//go:build !unix

package couchdb

func (s *Service) killErlangOrphans() {}
