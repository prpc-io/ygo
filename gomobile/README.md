# gomobile binding

Bytes-only subset of ygo for use with [`gomobile bind`](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile),
the official Go cross-compilation toolchain for iOS and Android.

The main `ygo` package exposes a fully idiomatic Go API (channels,
`any`, callbacks, generics) that `gomobile bind` cannot generate
bindings for. This subpackage wraps the underlying types with
bytes-in / bytes-out methods only — everything maps cleanly onto
the JavaScript-style API surface that Objective-C / Java consumers
can call.

## Verified iOS xcframework build

```bash
# One-time setup
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
$(go env GOPATH)/bin/gomobile init

# In a fresh checkout of github.com/Deln0r/ygo:
go get golang.org/x/mobile/bind   # gomobile build dependency
$(go env GOPATH)/bin/gomobile bind -target=ios,iossimulator \
    -o /tmp/Ygo.xcframework \
    github.com/Deln0r/ygo/gomobile
```

Produces a `.xcframework` containing:
- `ios-arm64/Ygo.framework` (~6.6 MB) — real-device slice (arm64)
- `ios-arm64_x86_64-simulator/Ygo.framework` (~13 MB) — simulator slice (arm64 + x86_64, fat)
- Auto-generated Objective-C headers in each slice's `Headers/` dir
  (`Ygo.h`, `Gomobile.objc.h`, `Universe.objc.h`, `ref.h`)

Drag the `.xcframework` into Xcode under "Frameworks, Libraries,
and Embedded Content"; the auto-generated Swift bridging header
exposes `GomobileDoc`, `GomobileAwareness` and helpers like
`GomobileNewDoc`, `GomobileNewDocWithClientID`. Verified on
Xcode 16+, Go 1.26, macOS 26 (Apple Silicon, May 2026).

## Verified Android AAR build

```bash
# Requires Android SDK + NDK side-by-side installed via Android Studio's
# SDK Manager (Tools → SDK Manager → SDK Tools → "NDK (Side by side)").
export ANDROID_HOME=$HOME/Library/Android/sdk
export ANDROID_NDK_HOME=$ANDROID_HOME/ndk/<version>

go get golang.org/x/mobile/bind
$(go env GOPATH)/bin/gomobile bind -target=android \
    -o /tmp/ygo.aar \
    github.com/Deln0r/ygo/gomobile
```

`gomobile bind -target=android` produces an `.aar` (Android
archive) consumable from any Gradle Android project. NDK
installation status as of v0.9: deferred per developer
preference — toolchain end-to-end verified for iOS first.

## Note on `go.mod`

`gomobile bind` requires `golang.org/x/mobile/bind` to be present
in the module's dependency graph at build time. The main `go.mod`
does NOT carry this dep (it would bump the `go` directive past
1.22 and break our CI Go-version matrix). Adopters running their
own `gomobile bind` should `go get golang.org/x/mobile/bind` in
their fresh checkout before the bind step (see commands above);
the dep is build-time only, no runtime cost.
