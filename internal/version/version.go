package version

import (
	"runtime/debug"
)

const (
	defaultVersion   = "dev"
	defaultCommit    = "none"
	defaultBuildDate = "unknown"
	defaultValue     = "unknown"
)

var (
	Version   = defaultVersion
	Commit    = defaultCommit
	BuildDate = defaultBuildDate
)

type VersionInfo struct {
	GoVersion string `json:"goVersion"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
}

func GetBuildInfo() VersionInfo {
	// Prefer ldflags-injected values if they were set
	if Version != defaultVersion || Commit != defaultCommit || BuildDate != defaultBuildDate {
		binfo, ok := debug.ReadBuildInfo()
		goVer := defaultValue
		if ok {
			goVer = binfo.GoVersion
		}
		return VersionInfo{
			GoVersion: goVer,
			Version:   Version,
			Commit:    Commit,
			BuildDate: BuildDate,
		}
	}

	// Fallback: ReadBuildInfo + vcs.*
	binfo, ok := debug.ReadBuildInfo()
	if !ok {
		return VersionInfo{
			GoVersion: defaultValue,
			Version:   defaultVersion,
			Commit:    defaultCommit,
			BuildDate: defaultBuildDate,
		}
	}

	return VersionInfo{
		GoVersion: binfo.GoVersion,
		Version:   binfo.Main.Version,
		Commit:    getBuildInfoSetting(binfo, "vcs.revision"),
		BuildDate: getBuildInfoSetting(binfo, "vcs.time"),
	}
}

func getBuildInfoSetting(info *debug.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}

	return defaultValue
}
