// Copyright (C) 2026 by Posit Software, PBC
package indexer

const maxSignatureLen = 200

func truncateSignature(sig string) string {
	if len(sig) <= maxSignatureLen {
		return sig
	}
	return sig[:maxSignatureLen] + "..."
}
