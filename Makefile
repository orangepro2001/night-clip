# night-clip build entry points. Run from the repository root.
#
#   make test        Go unit tests (race detector on)
#   make vet         go vet + gofmt check
#   make dll         Windows x64 clipbridge_core.dll + header (needs mingw-w64)
#   make dll-smoke   build the Linux .so and run the C ABI smoke test
#   make dll-wine    run the Windows DLL under Wine (needs wine64)
#   make test-interop  C# interop tests against the Linux .so (needs a .NET SDK)
#   make build-windows-app  compile the WPF app on Linux (EnableWindowsTargeting)
#   make apk         debug APK (needs JDK 17, Android SDK, make aar first)
#   make cli         cbridge.exe (Windows) and cbridge (host)
#   make aar         Android AAR (needs gomobile + Android NDK, see below)
#   make clean

GO        ?= go
DOTNET    ?= $(shell command -v dotnet || echo $$HOME/.dotnet/dotnet)
MINGW_CC  ?= x86_64-w64-mingw32-gcc
DIST      := core/dist
JAVAPKG   ?= com.nightclip.core
ANDROIDAPI ?= 26

# Android toolchain locations (override on other machines).
export ANDROID_HOME     ?= $(HOME)/Android/Sdk
export ANDROID_NDK_HOME ?= $(lastword $(sort $(wildcard $(ANDROID_HOME)/ndk/*)))
export JAVA_HOME        ?= /usr/lib/jvm/java-17-openjdk-amd64
export PATH := $(PATH):$(shell $(GO) env GOPATH)/bin

.PHONY: all test vet dll dll-smoke dll-wine test-interop build-windows-app cli aar apk check clean

all: test dll cli

test:
	cd core && $(GO) test -race ./...

vet:
	cd core && $(GO) vet ./...
	@out="$$(cd core && gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

dll:
	mkdir -p $(DIST)/windows-amd64
	cd core && CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=$(MINGW_CC) \
		$(GO) build -buildmode=c-shared -trimpath -ldflags="-s -w" \
		-o dist/windows-amd64/clipbridge_core.dll ./cmd/cbcore-dll

dll-smoke:
	mkdir -p $(DIST)/linux-amd64
	cd core && CGO_ENABLED=1 $(GO) build -buildmode=c-shared \
		-o dist/linux-amd64/libclipbridge_core.so ./cmd/cbcore-dll
	gcc -Wall -Wextra -o $(DIST)/linux-amd64/smoke core/test/abi/smoke.c \
		-I$(DIST)/linux-amd64 -L$(DIST)/linux-amd64 -lclipbridge_core \
		-Wl,-rpath,'$$ORIGIN'
	$(DIST)/linux-amd64/smoke

dll-wine: dll
	$(MINGW_CC) -Wall -Wextra -Wno-cast-function-type -o $(DIST)/windows-amd64/smoke_win.exe core/test/abi/smoke_win.c
	cd $(DIST)/windows-amd64 && WINEDEBUG=-all WINEPREFIX=$${WINEPREFIX:-$$HOME/.wine-clipbridge} \
		wine64 smoke_win.exe clipbridge_core.dll

test-interop: dll-smoke
	DOTNET_CLI_TELEMETRY_OPTOUT=1 $(DOTNET) test windows/ClipBridge.Windows.Tests

build-windows-app: dll
	DOTNET_CLI_TELEMETRY_OPTOUT=1 $(DOTNET) build windows/ClipBridge.Windows \
		-p:EnableWindowsTargeting=true -c Release

cli:
	mkdir -p $(DIST)/windows-amd64 $(DIST)/host
	cd core && GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags="-s -w" \
		-o dist/windows-amd64/cbridge.exe ./cmd/cbridge
	cd core && $(GO) build -o dist/host/cbridge ./cmd/cbridge

# Prerequisites (not installed by this repo):
#   go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init
#   ANDROID_HOME and ANDROID_NDK_HOME set
# Output is copied into the Android app's libs/ directory.
aar:
	@command -v gomobile >/dev/null || { echo "gomobile not found; see comment above"; exit 1; }
	mkdir -p android/app/libs
	cd core && gomobile bind -target=android/arm64,android/amd64 -androidapi $(ANDROIDAPI) \
		-javapkg $(JAVAPKG) -o ../android/app/libs/clipbridge-core.aar ./mobile

apk: aar
	cd android && ./gradlew --no-daemon :app:assembleDebug

# Everything that can be verified on a Linux machine without a device.
check: vet test dll-smoke dll-wine test-interop build-windows-app cli aar apk

clean:
	rm -rf $(DIST) android/app/libs/*.aar
