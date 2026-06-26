package updates

import (
	"errors"
	"testing"
)

func TestSelectPlatformAsset_Windows(t *testing.T) {
	assets := []githubAsset{
		{Name: "Silt-v0.5.0-windows-portable.zip", BrowserDownloadURL: "u1"},
		{Name: "Silt-v0.5.0-windows-installer.exe", BrowserDownloadURL: "u2", Size: 9},
	}
	got, err := SelectPlatformAsset(assets, "windows")
	if err != nil {
		t.Fatalf("windows: %v", err)
	}
	if got.Name != "Silt-v0.5.0-windows-installer.exe" {
		t.Errorf("windows: got %q, want the .exe installer (not the .zip)", got.Name)
	}
	if got.Size != 9 {
		t.Errorf("windows: size = %d, want 9", got.Size)
	}
}

func TestSelectPlatformAsset_Linux(t *testing.T) {
	assets := []githubAsset{
		{Name: "silt_0.5.0_debian_amd64.deb", BrowserDownloadURL: "u1"},
		{Name: "Silt-0.5.0-linux-x86_64.AppImage", BrowserDownloadURL: "u2"},
	}
	got, err := SelectPlatformAsset(assets, "linux")
	if err != nil {
		t.Fatalf("linux: %v", err)
	}
	if got.Name != "Silt-0.5.0-linux-x86_64.AppImage" {
		t.Errorf("linux: got %q, want the .AppImage (not the .deb)", got.Name)
	}
}

func TestSelectPlatformAsset_UnsupportedOS(t *testing.T) {
	assets := []githubAsset{{Name: "a.exe", BrowserDownloadURL: "u"}}
	_, err := SelectPlatformAsset(assets, "darwin")
	if err == nil {
		t.Fatal("darwin: expected ErrPlatformNotSupported")
	}
	if !errors.Is(err, ErrPlatformNotSupported) {
		t.Errorf("darwin: expected ErrPlatformNotSupported, got %v", err)
	}
}

func TestSelectPlatformAsset_NoMatch(t *testing.T) {
	// A windows asset but we ask for linux → no match → unsupported.
	assets := []githubAsset{{Name: "a.exe", BrowserDownloadURL: "u"}}
	_, err := SelectPlatformAsset(assets, "linux")
	if err == nil {
		t.Fatal("expected ErrPlatformNotSupported when no matching asset")
	}
	if !errors.Is(err, ErrPlatformNotSupported) {
		t.Errorf("expected ErrPlatformNotSupported, got %v", err)
	}
}
