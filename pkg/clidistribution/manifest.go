// Package clidistribution defines the public CLI distribution contract.
package clidistribution

type Asset struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	Version string           `json:"version"`
	Assets  map[string]Asset `json:"assets"`
}
