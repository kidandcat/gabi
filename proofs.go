package credential

import (
	"errors"
	"math/big"
)

// Proof represents a non-interactive zero-knowledge proof
type Proof interface {
	VerifyWithChallenge(pk *PublicKey, reconstructedChallenge *big.Int) bool
	SecretKeyResponse() *big.Int
	ChallengeContribution(pk *PublicKey) []*big.Int
}

// createChallenge creates a challenge based on context, nonce and the
// contributions.
func createChallenge(context, nonce *big.Int, contributions []*big.Int) *big.Int {
	// Basically, sandwich the contributions between context and nonce
	input := make([]*big.Int, 2+len(contributions))
	input[0] = context
	copy(input[1:1+len(contributions)], contributions)
	input[len(input)-1] = nonce
	return hashCommit(input)
}

// ProofU represents a proof of correctness of the commitment in the first phase
// of the issuance protocol.
type ProofU struct {
	u              *big.Int
	c              *big.Int
	vPrimeResponse *big.Int
	sResponse      *big.Int
}

// Verify verifies whether the proof is correct.
func (p *ProofU) Verify(pk *PublicKey, context, nonce *big.Int) bool {
	return p.VerifyWithChallenge(pk, createChallenge(context, nonce, p.ChallengeContribution(pk)))
}

// correctResponseSizes checks the sizes of the elements in the ProofU proof.
func (p *ProofU) correctResponseSizes(pk *PublicKey) bool {
	maximum := new(big.Int).Lsh(bigONE, pk.Params.LvPrimeCommit+1)
	maximum.Sub(maximum, bigONE)
	minimum := new(big.Int).Neg(maximum)

	return p.vPrimeResponse.Cmp(minimum) >= 0 && p.vPrimeResponse.Cmp(maximum) <= 0
}

// VerifyWithChallenge verifies whether the proof is correct.
func (p *ProofU) VerifyWithChallenge(pk *PublicKey, reconstructedChallenge *big.Int) bool {
	return p.correctResponseSizes(pk) && p.c.Cmp(reconstructedChallenge) == 0
}

// reconstructUcommit reconstructs U from the information in the proof and the
// provided public key.
func (p *ProofU) reconstructUcommit(pk *PublicKey) *big.Int {
	// Reconstruct Ucommit
	// U_commit = U^{-c} * S^{vPrimeResponse} * R_0^{sResponse}
	Uc := modPow(p.u, new(big.Int).Neg(p.c), &pk.N)
	Sv := modPow(&pk.S, p.vPrimeResponse, &pk.N)
	R0s := modPow(pk.R[0], p.sResponse, &pk.N)
	Ucommit := new(big.Int).Mul(Uc, Sv)
	Ucommit.Mul(Ucommit, R0s).Mod(Ucommit, &pk.N)

	return Ucommit
}

// SecretKeyResponse returns the secret key response (as part of Proof
// interface).
func (p *ProofU) SecretKeyResponse() *big.Int {
	return p.sResponse
}

// Challenge returns the challenge in the proof (part of the Proof interface).
func (p *ProofU) Challenge() *big.Int {
	return p.c
}

// ChallengeContribution returns the contribution of this proof to the
// challenge.
func (p *ProofU) ChallengeContribution(pk *PublicKey) []*big.Int {
	return []*big.Int{p.u, p.reconstructUcommit(pk)}
}

// ProofS represents a proof.
type ProofS struct {
	c         *big.Int
	eResponse *big.Int
}

// Verify verifies the proof agains the given public key, signature, context,
// and nonce.
func (p *ProofS) Verify(pk *PublicKey, signature *CLSignature, context, nonce *big.Int) bool {
	// Reconstruct A_commit
	// ACommit = A^{c + eResponse * e}
	exponent := new(big.Int).Mul(p.eResponse, signature.E)
	exponent.Add(p.c, exponent)
	ACommit := new(big.Int).Exp(signature.A, exponent, &pk.N)

	// Reconstruct Q
	Q := new(big.Int).Exp(signature.A, signature.E, &pk.N)

	// Recalculate hash
	cPrime := hashCommit([]*big.Int{context, Q, signature.A, nonce, ACommit})

	return p.c.Cmp(cPrime) == 0
}

// ProofD represents a proof in the showing protocol.
type ProofD struct {
	c, A, eResponse, vResponse *big.Int
	aResponses, aDisclosed     map[int]*big.Int
}

// correctResponseSizes checks the sizes of the elements in the ProofD proof.
func (p *ProofD) correctResponseSizes(pk *PublicKey) bool {
	// Check range on the aResponses
	maximum := new(big.Int).Lsh(bigONE, pk.Params.LmCommit+1)
	maximum.Sub(maximum, bigONE)
	minimum := new(big.Int).Neg(maximum)
	for _, aResponse := range p.aResponses {
		if aResponse.Cmp(minimum) < 0 || aResponse.Cmp(maximum) > 0 {
			return false
		}
	}

	// Check range eResponse
	maximum.Lsh(bigONE, pk.Params.LeCommit+1)
	maximum.Sub(maximum, bigONE)
	minimum.Neg(maximum)

	if p.eResponse.Cmp(minimum) < 0 || p.eResponse.Cmp(maximum) > 0 {
		return false
	}

	return true
}

// reconstructZ reconstructs Z from the information in the proof and the
// provided public key.
func (p *ProofD) reconstructZ(pk *PublicKey) *big.Int {
	// known = Z / ( prod_{disclosed} R_i^{a_i} * A^{2^{l_e - 1}} )
	numerator := new(big.Int).Lsh(bigONE, pk.Params.Le-1)
	numerator.Exp(p.A, numerator, &pk.N)
	for i, attribute := range p.aDisclosed {
		numerator.Mul(numerator, new(big.Int).Exp(pk.R[i], attribute, &pk.N))
	}

	known := new(big.Int).ModInverse(numerator, &pk.N)
	known.Mul(&pk.Z, known)

	knownC := modPow(known, new(big.Int).Neg(p.c), &pk.N)
	Ae := modPow(p.A, p.eResponse, &pk.N)
	Sv := modPow(&pk.S, p.vResponse, &pk.N)
	Rs := big.NewInt(1)
	for i, response := range p.aResponses {
		Rs.Mul(Rs, modPow(pk.R[i], response, &pk.N))
	}
	Z := new(big.Int).Mul(knownC, Ae)
	Z.Mul(Z, Rs).Mul(Z, Sv).Mod(Z, &pk.N)

	return Z
}

// Verify verifies the proof against the given public key, context, and nonce.
func (p *ProofD) Verify(pk *PublicKey, context, nonce1 *big.Int) bool {
	return p.VerifyWithChallenge(pk, createChallenge(context, nonce1, p.ChallengeContribution(pk)))
}

// Verify verifies the proof against the given public key and the provided
// reconstruted challenge.
func (p *ProofD) VerifyWithChallenge(pk *PublicKey, reconstructedChallenge *big.Int) bool {
	return p.correctResponseSizes(pk) && p.c.Cmp(reconstructedChallenge) == 0
}

// ChallengeContribution returns the contribution of this proof to the
// challenge.
func (p *ProofD) ChallengeContribution(pk *PublicKey) []*big.Int {
	return []*big.Int{p.A, p.reconstructZ(pk)}
}

// SecretKeyResponse returns the secret key response (as part of Proof
// interface).
func (p *ProofD) SecretKeyResponse() *big.Int {
	return p.aResponses[0]
}

// Challenge returns the challenge in the proof (part of the Proof interface).
func (p *ProofD) Challenge() *big.Int {
	return p.c
}

// ProofBuilder is an interface for a prof builder. That is, an object to hold
// the state to build a list of bounded proofs (see ProofList).
type ProofBuilder interface {
	Commit(skRandomizer *big.Int) []*big.Int
	CreateProof(challenge *big.Int) Proof
}

// ProofList represents a list of (typically bounded) proofs.
type ProofList []Proof

var (
	// ErrMissingProofU is returned when a ProofU proof is missing in a prooflist
	// when this is expected.
	ErrMissingProofU = errors.New("Missing ProofU in ProofList, has a CredentialBuilder been added?")
)

// GetProofU returns the n'th ProofU in this proof list.
func (pl ProofList) GetProofU(n int) (*ProofU, error) {
	count := 0
	for _, proof := range pl {
		switch proof.(type) {
		case *ProofU:
			if count == n {
				return proof.(*ProofU), nil
			}
			count++
		}
	}
	return nil, ErrMissingProofU
}

// GetFirstProofU returns the first ProofU in this proof list
func (pl ProofList) GetFirstProofU() (*ProofU, error) {
	return pl.GetProofU(0)
}

// challengeContributions collects and returns all the challenge contributions
// of the proofs contained in the proof list.
func (pl ProofList) challengeContributions(publicKeys []*PublicKey, context, nonce *big.Int) []*big.Int {
	contributions := make([]*big.Int, 0, len(pl)*2)
	for i, proof := range pl {
		contributions = append(contributions, proof.ChallengeContribution(publicKeys[i])...)
	}
	return contributions
}

// Verify returns true when all the proofs inside verify and if shouldBeBound is
// set to true whether all proofs are properly bound.
func (pl ProofList) Verify(publicKeys []*PublicKey, context, nonce *big.Int, shouldBeBound bool) bool {
	if len(pl) == 0 {
		return true
	}

	if len(pl) != len(publicKeys) {
		return false
	}

	if shouldBeBound {
		contributions := pl.challengeContributions(publicKeys, context, nonce)
		expectedChallenge := createChallenge(context, nonce, contributions)
		expectedSecretKeyResponse := pl[0].SecretKeyResponse()
		for i, proof := range pl {
			if expectedSecretKeyResponse.Cmp(proof.SecretKeyResponse()) != 0 ||
				!proof.VerifyWithChallenge(publicKeys[i], expectedChallenge) {
				return false
			}
		}
	} else {
		for i, proof := range pl {
			// if !proof.Verify(publicKeys[i], context, nonce) {
			if !proof.VerifyWithChallenge(publicKeys[i], createChallenge(context, nonce, proof.ChallengeContribution(publicKeys[i]))) {
				return false
			}
		}
	}

	return true
}

// BuildProofList builds a list of bounded proofs. For this it is given a list
// of ProofBuilders. Examples of proof builders are Builder and
// DisclosureProofBuilder.
func BuildProofList(params *SystemParameters, context, nonce *big.Int, proofBuilders []ProofBuilder) ProofList {
	skCommitment, _ := randomBigInt(params.LmCommit)

	commitmentValues := make([]*big.Int, 0, len(proofBuilders)*2)
	for _, pb := range proofBuilders {
		commitmentValues = append(commitmentValues, pb.Commit(skCommitment)...)
	}

	// Create a shared challenge
	challenge := createChallenge(context, nonce, commitmentValues)

	proofs := make([]Proof, len(proofBuilders))
	// Now create proofs using this challenge
	for i, v := range proofBuilders {
		proofs[i] = v.CreateProof(challenge)
	}
	return proofs
}
