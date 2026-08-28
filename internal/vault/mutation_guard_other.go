//go:build !darwin

package vault

// mutationLocationPolicy is nil everywhere except macOS, so GuardMutation is a
// no-op on these platforms.
//
// The policy is not a general "is this storage trustworthy" check that happens
// to be implemented for one OS. It tests for one specific arrangement — a
// directory under a macOS File Provider domain, where a path that goes vacant
// is replicated as a deletion — and there is no equivalent attribute to consult
// on Linux or Windows. Guessing at a substitute would refuse ordinary local
// vaults without making any real storage safer.
//
// What still applies on every platform is the safe-replacement contract: a
// write proves the destination volume supports a path-preserving exchange
// before it uses one, and fails closed when it does not.
var mutationLocationPolicy mutationLocationPolicyFunc
