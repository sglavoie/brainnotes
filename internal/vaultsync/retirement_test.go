package vaultsync

import (
	"errors"
	"testing"
)

func TestRetirementRoundTripsAndOnlyAdvances(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	record := &Retirement{
		Format:       Format,
		VaultID:      r.peer.VaultID,
		OriginDevice: r.peer.DeviceID,
		Epoch:        r.peer.Epoch,
		Through:      2,
		Retained: []RetainedPackage{
			{PackageID: "pkg-" + "11111111111111111111111111111111", Sequence: 1},
		},
	}
	path, err := PublishRetirement(channel, record)
	if err != nil {
		t.Fatalf("PublishRetirement: %v", err)
	}
	if path != RetirementPath(channel) {
		t.Fatalf("retirement path = %q, want %q", path, RetirementPath(channel))
	}
	written, err := ReadRetirement(channel)
	if err != nil {
		t.Fatalf("ReadRetirement: %v", err)
	}
	if err := written.VerifyFor(r.local); err != nil {
		t.Fatalf("VerifyFor: %v", err)
	}
	advanceRetirement(t, channel, record)
}

func advanceRetirement(t *testing.T, channel string, record *Retirement) {
	t.Helper()
	advanced := *record
	advanced.Through = 5
	advanced.Retained = nil
	if _, err := PublishRetirement(channel, &advanced); err != nil {
		t.Fatalf("advance retirement: %v", err)
	}
	written, err := ReadRetirement(channel)
	if err != nil || written.Through != 5 {
		t.Fatalf("advanced retirement = %+v, %v; want through 5", written, err)
	}
	if written.Retains(record.Retained[0].PackageID) {
		t.Fatal("advanced retirement still retains a package the sender removed")
	}
}

func TestRetirementRefusesRegression(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	record := &Retirement{
		Format:       Format,
		VaultID:      r.peer.VaultID,
		OriginDevice: r.peer.DeviceID,
		Epoch:        r.peer.Epoch,
		Through:      5,
	}
	if _, err := PublishRetirement(channel, record); err != nil {
		t.Fatalf("PublishRetirement: %v", err)
	}
	regressed := *record
	regressed.Through = 4
	if _, err := PublishRetirement(channel, &regressed); !errors.Is(err, ErrMalformedPackage) {
		t.Fatalf("regression error = %v, want ErrMalformedPackage", err)
	}
	written, err := ReadRetirement(channel)
	if err != nil || written.Through != 5 {
		t.Fatalf("regression changed retirement to %+v, %v", written, err)
	}
}

func TestRetirementVerificationRequiresTheEnrolledOriginAndEpoch(t *testing.T) {
	r := enroll(t)
	record := &Retirement{
		Format:       Format,
		VaultID:      r.peer.VaultID,
		OriginDevice: r.peer.DeviceID,
		Epoch:        r.peer.Epoch,
		Through:      1,
	}
	if err := record.VerifyFor(r.local); err != nil {
		t.Fatalf("valid retirement: %v", err)
	}

	wrongOrigin := *record
	wrongOrigin.OriginDevice = r.local.DeviceID
	if err := wrongOrigin.VerifyFor(r.local); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("self-origin error = %v, want ErrUnknownPeer", err)
	}

	wrongEpoch := *record
	wrongEpoch.Epoch++
	if err := wrongEpoch.VerifyFor(r.local); !errors.Is(err, ErrEpochAhead) {
		t.Fatalf("future epoch error = %v, want ErrEpochAhead", err)
	}
}
