// Copyright 2024 The go-github AUTHORS. All rights reserved.
//
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// This is a simple example of how to verify an artifact
// attestations hosted on GitHub using the sigstore-go library.
// This is a very barebones example drawn from the sigstore-go
// library's examples and should not be used in production.

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/go-github/v66/github"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

var (
	owner                   = flag.String("owner", "cli", "GitHub organization or user to scope attestation lookup by")
	artifactDigest          = flag.String("artifact-digest", "2ce2e480e3c3f7ca0af83418d3ebaeedacee135dbac94bd946d7d84edabcdb64", "digest of the artifact")
	artifactDigestAlgorithm = flag.String("artifact-digest-algorithm", "sha256", "The algorithm used to compute the digest of the artifact")
	expectedIssuer          = flag.String("expected-issuer", "https://token.actions.githubusercontent.com", "Issuer of the OIDC token")
	expectedSAN             = flag.String("expected-san", "https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk", "The expected Subject Alternative Name (SAN) of the certificate used to sign the attestation")
	trustedRootJSONPath     = flag.String("trusted-root-json-path", "verifyartifact/trusted-root-public-good.json", "Path to the trusted root JSON file")
)

func main() {
	flag.Parse()
	token := os.Getenv("GITHUB_AUTH_TOKEN")

	if token == "" {
		log.Fatal("Unauthorized: No token present")
	}

	ctx := context.Background()
	client := github.NewClient(nil).WithAuthToken(token)

	// Fetch attestations from the GitHub API.
	// The API doesn't differentiate between users and orgs,
	// so we can use the OrganizationsService to fetch attestations for both.
	//
	// Note: The GitHub API only seems to support querying by sha256 digest.
	attestations, _, err := client.Organizations.ListAttestations(ctx, *owner, fmt.Sprintf("%v:%v", *artifactDigestAlgorithm, *artifactDigest), nil)
	if err != nil {
		log.Fatal(err)
	}

	if len(attestations.Attestations) == 0 {
		log.Fatal("No attestations found")
	}

	sev, err := getSignedEntityVerifier()
	if err != nil {
		log.Fatal(err)
	}

	pb, err := getPolicyBuilder()
	if err != nil {
		log.Fatal(err)
	}

	var b *bundle.Bundle
	for _, attestation := range attestations.Attestations {
		if err := json.Unmarshal(*attestation.Bundle, &b); err != nil {
			log.Fatal(err)
		}

		err := runVerification(sev, pb, b)

		if err != nil {
			log.Fatal(err)
		}
	}
}

func getTrustedMaterial() (root.TrustedMaterialCollection, error) {
	trustedRootJSON, err := os.ReadFile(*trustedRootJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", *trustedRootJSONPath, err)
	}

	trustedRoot, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		return nil, err
	}

	trustedMaterial := root.TrustedMaterialCollection{
		trustedRoot,
	}

	return trustedMaterial, nil
}

func getIdentityPolicies() ([]verify.PolicyOption, error) {
	certID, err := verify.NewShortCertificateIdentity(*expectedIssuer, "", *expectedSAN, "")
	if err != nil {
		return nil, err
	}

	return []verify.PolicyOption{
		verify.WithCertificateIdentity(certID),
	}, nil
}

func getSignedEntityVerifier() (*verify.SignedEntityVerifier, error) {
	// Set up the verifier
	verifierConfig := []verify.VerifierOption{
		verify.WithSignedCertificateTimestamps(1),
		verify.WithObserverTimestamps(1),
		verify.WithTransparencyLog(1),
	}

	// Set up the trusted material
	trustedMaterial, err := getTrustedMaterial()
	if err != nil {
		return nil, err
	}

	return verify.NewSignedEntityVerifier(trustedMaterial, verifierConfig...)
}

func getPolicyBuilder() (*verify.PolicyBuilder, error) {
	// Set up the identity policy
	identityPolicies, err := getIdentityPolicies()
	if err != nil {
		return nil, err
	}

	// Set up the articaft policy
	artifactDigestBytes, err := hex.DecodeString(*artifactDigest)
	if err != nil {
		return nil, err
	}
	artifactPolicy := verify.WithArtifactDigest(*artifactDigestAlgorithm, artifactDigestBytes)

	pb := verify.NewPolicy(artifactPolicy, identityPolicies...)
	return &pb, nil
}

func runVerification(sev *verify.SignedEntityVerifier, pb *verify.PolicyBuilder, b *bundle.Bundle) error {
	res, err := sev.Verify(b, *pb)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Verification successful!\n")

	marshaled, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(marshaled))
	return nil
}
