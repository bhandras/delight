SHELL := /bin/bash

SIMULATOR_DEVICE ?= iPhone 16
CONFIGURATION ?= Debug
DERIVED_DATA ?= /tmp/delight-ios
BUNDLED_GOCACHE ?= $(CURDIR)/.gocache
BUNDLED_GOMODCACHE ?= $(CURDIR)/.gomodcache
BUNDLED_GOLANGCI_CACHE ?= $(CURDIR)/.golangci-cache
BUNDLE_ID ?=
APP_PATH := $(DERIVED_DATA)/Build/Products/$(CONFIGURATION)-iphonesimulator/Delight.app
SIMULATOR_UDID_FILE := $(DERIVED_DATA)/booted_simulator_udid
IOS_PROJECT ?= ios/DelightApp.xcodeproj
IOS_SCHEME ?= DelightApp
IOS_RELEASE_CONFIGURATION ?= Release
IOS_ARCHIVE_PATH ?= $(DERIVED_DATA)/archive/$(IOS_SCHEME).xcarchive
IOS_EXPORT_PATH ?= $(DERIVED_DATA)/export
IOS_EXPORT_OPTIONS_PLIST ?= $(DERIVED_DATA)/ExportOptions.plist
IOS_EXPORT_METHOD ?= app-store-connect
IOS_EXPORT_SIGNING_STYLE ?= automatic
IOS_PROFILE_NAME ?=
IOS_SIGNING_CERT ?= Apple Distribution
ASC_API_KEY_ID ?=
ASC_API_ISSUER_ID ?=
ASC_API_KEY_PATH ?=
ASC_APPLE_ID ?=
ASC_PUBLIC_ID ?=
ASC_OUTPUT_FORMAT ?= normal
SERVER_DOCKER_IMAGE ?= delight-server:local

.PHONY: ios-sdk ios-build ios-sim-boot ios-install ios-run ios-release-archive ios-export-ipa ios-testflight-upload
CLI_TEST_PKGS ?= ./...
SERVER_TEST_PKGS ?= ./...
WEBCLIENT_TEST_PKGS ?= ./...
GO_TEST_ARGS ?= -cover
IOS_TEST_RESULT ?= $(DERIVED_DATA)/TestResults

ios-sdk:
	./cli/scripts/build_ios_sdk.sh

ios-build: ios-sdk
	xcodebuild \
		-project ios/DelightApp.xcodeproj \
		-scheme DelightApp \
		-configuration $(CONFIGURATION) \
		-sdk iphonesimulator \
		-destination "platform=iOS Simulator,name=$(SIMULATOR_DEVICE)" \
		-derivedDataPath "$(DERIVED_DATA)" \
		build

ios-sim-boot:
	@mkdir -p "$(DERIVED_DATA)"
	@./cli/scripts/ensure_ios_sim_booted.sh "$(SIMULATOR_DEVICE)" > "$(SIMULATOR_UDID_FILE)"
	@echo "Booted simulator UDID: $$(cat "$(SIMULATOR_UDID_FILE)")"
	@UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	open -a Simulator --args -CurrentDeviceUDID "$$UDID" >/dev/null 2>&1 || true

ios-install: ios-build ios-sim-boot
	@UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	xcrun simctl install "$$UDID" "$(APP_PATH)"

ios-run: ios-install
	@set -euo pipefail; \
	UDID="$$(cat "$(SIMULATOR_UDID_FILE)")"; \
	bundle_id="$(BUNDLE_ID)"; \
	if [[ -z "$$bundle_id" ]]; then \
		bundle_id="$$(/usr/libexec/PlistBuddy -c "Print :CFBundleIdentifier" "$(APP_PATH)/Info.plist" 2>/dev/null || true)"; \
	fi; \
	if [[ -z "$$bundle_id" ]]; then \
		echo "error: unable to determine app bundle id. Build output may be missing at $(APP_PATH)."; \
		exit 1; \
	fi; \
	echo "Launching $$bundle_id on simulator $$UDID..."; \
	xcrun simctl launch "$$UDID" "$$bundle_id"

ios-release-archive: ios-sdk
	rm -rf "$(IOS_ARCHIVE_PATH)"
	xcodebuild \
		-project "$(IOS_PROJECT)" \
		-scheme "$(IOS_SCHEME)" \
		-configuration "$(IOS_RELEASE_CONFIGURATION)" \
		-destination "generic/platform=iOS" \
		-archivePath "$(IOS_ARCHIVE_PATH)" \
		-allowProvisioningUpdates \
		archive

ios-export-ipa: ios-release-archive
	@set -euo pipefail; \
	archive_info_plist="$(IOS_ARCHIVE_PATH)/Info.plist"; \
	if [[ ! -f "$$archive_info_plist" ]]; then \
		echo "error: archive metadata not found at $$archive_info_plist"; \
		exit 1; \
	fi; \
	bundle_id="$$(/usr/libexec/PlistBuddy -c "Print :ApplicationProperties:CFBundleIdentifier" "$$archive_info_plist" 2>/dev/null || true)"; \
	if [[ -z "$$bundle_id" ]]; then \
		echo "error: failed to read archive bundle id from $$archive_info_plist"; \
		exit 1; \
	fi; \
	mkdir -p "$(DERIVED_DATA)"; \
	signing_style="$(IOS_EXPORT_SIGNING_STYLE)"; \
	if [[ -n "$(IOS_PROFILE_NAME)" ]]; then \
		signing_style="manual"; \
	fi; \
	if [[ "$$signing_style" == "manual" ]]; then \
		if [[ -z "$(IOS_PROFILE_NAME)" ]]; then \
			echo "error: IOS_PROFILE_NAME is required when IOS_EXPORT_SIGNING_STYLE=manual."; \
			exit 1; \
		fi; \
		printf '%s\n' \
			'<?xml version="1.0" encoding="UTF-8"?>' \
			'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
			'<plist version="1.0">' \
			'<dict>' \
			'    <key>destination</key>' \
			'    <string>export</string>' \
			'    <key>method</key>' \
			'    <string>$(IOS_EXPORT_METHOD)</string>' \
			'    <key>signingStyle</key>' \
			'    <string>manual</string>' \
			'    <key>signingCertificate</key>' \
			'    <string>$(IOS_SIGNING_CERT)</string>' \
			'    <key>provisioningProfiles</key>' \
			'    <dict>' \
			"        <key>$$bundle_id</key>" \
			'        <string>$(IOS_PROFILE_NAME)</string>' \
			'    </dict>' \
			'    <key>stripSwiftSymbols</key>' \
			'    <true/>' \
			'    <key>manageAppVersionAndBuildNumber</key>' \
			'    <false/>' \
			'</dict>' \
			'</plist>' \
			> "$(IOS_EXPORT_OPTIONS_PLIST)"; \
	else \
		printf '%s\n' \
			'<?xml version="1.0" encoding="UTF-8"?>' \
			'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
			'<plist version="1.0">' \
			'<dict>' \
			'    <key>destination</key>' \
			'    <string>export</string>' \
			'    <key>method</key>' \
			'    <string>$(IOS_EXPORT_METHOD)</string>' \
			'    <key>signingStyle</key>' \
			'    <string>automatic</string>' \
			'    <key>stripSwiftSymbols</key>' \
			'    <true/>' \
			'    <key>manageAppVersionAndBuildNumber</key>' \
			'    <false/>' \
			'</dict>' \
			'</plist>' \
			> "$(IOS_EXPORT_OPTIONS_PLIST)"; \
	fi
	rm -rf "$(IOS_EXPORT_PATH)"
	xcodebuild \
		-exportArchive \
		-archivePath "$(IOS_ARCHIVE_PATH)" \
		-exportPath "$(IOS_EXPORT_PATH)" \
		-exportOptionsPlist "$(IOS_EXPORT_OPTIONS_PLIST)" \
		-allowProvisioningUpdates

ios-testflight-upload: ios-export-ipa
	@set -euo pipefail; \
	if [[ -z "$(ASC_API_KEY_ID)" ]]; then \
		echo "error: ASC_API_KEY_ID is required."; \
		exit 1; \
	fi; \
	if [[ -z "$(ASC_API_ISSUER_ID)" ]]; then \
		echo "error: ASC_API_ISSUER_ID is required."; \
		exit 1; \
	fi; \
	if [[ -z "$(ASC_APPLE_ID)" ]]; then \
		echo "error: ASC_APPLE_ID is required (numeric App Store Connect app ID)."; \
		exit 1; \
	fi; \
	archive_info_plist="$(IOS_ARCHIVE_PATH)/Info.plist"; \
	if [[ ! -f "$$archive_info_plist" ]]; then \
		echo "error: archive metadata not found at $$archive_info_plist"; \
		exit 1; \
	fi; \
	bundle_id="$$(/usr/libexec/PlistBuddy -c "Print :ApplicationProperties:CFBundleIdentifier" "$$archive_info_plist" 2>/dev/null || true)"; \
	bundle_version="$$(/usr/libexec/PlistBuddy -c "Print :ApplicationProperties:CFBundleVersion" "$$archive_info_plist" 2>/dev/null || true)"; \
	bundle_short_version="$$(/usr/libexec/PlistBuddy -c "Print :ApplicationProperties:CFBundleShortVersionString" "$$archive_info_plist" 2>/dev/null || true)"; \
	if [[ -z "$$bundle_id" || -z "$$bundle_version" || -z "$$bundle_short_version" ]]; then \
		echo "error: failed to read bundle metadata from $$archive_info_plist"; \
		exit 1; \
	fi; \
	ipa_path="$$(find "$(IOS_EXPORT_PATH)" -maxdepth 1 -name '*.ipa' -print -quit)"; \
	if [[ -z "$$ipa_path" ]]; then \
		echo "error: no .ipa found under $(IOS_EXPORT_PATH)."; \
		exit 1; \
	fi; \
	if [[ -n "$(ASC_API_KEY_PATH)" ]]; then \
		keys_dir="$(DERIVED_DATA)/private_keys"; \
		mkdir -p "$$keys_dir"; \
		install -m 600 "$(ASC_API_KEY_PATH)" "$$keys_dir/AuthKey_$(ASC_API_KEY_ID).p8"; \
		export API_PRIVATE_KEYS_DIR="$$keys_dir"; \
	fi; \
	echo "Uploading $$ipa_path to TestFlight..."; \
	if [[ -n "$(ASC_PUBLIC_ID)" ]]; then \
		xcrun altool \
			--upload-package "$$ipa_path" \
			--type ios \
			--asc-public-id "$(ASC_PUBLIC_ID)" \
			--apple-id "$(ASC_APPLE_ID)" \
			--bundle-id "$$bundle_id" \
			--bundle-version "$$bundle_version" \
			--bundle-short-version-string "$$bundle_short_version" \
			--apiKey "$(ASC_API_KEY_ID)" \
			--apiIssuer "$(ASC_API_ISSUER_ID)" \
			--show-progress \
			--output-format "$(ASC_OUTPUT_FORMAT)"; \
	else \
		xcrun altool \
			--upload-package "$$ipa_path" \
			--type ios \
			--apple-id "$(ASC_APPLE_ID)" \
			--bundle-id "$$bundle_id" \
			--bundle-version "$$bundle_version" \
			--bundle-short-version-string "$$bundle_short_version" \
			--apiKey "$(ASC_API_KEY_ID)" \
			--apiIssuer "$(ASC_API_ISSUER_ID)" \
			--show-progress \
			--output-format "$(ASC_OUTPUT_FORMAT)"; \
	fi

.PHONY: ios-test test

ios-test: ios-sdk ios-sim-boot
	rm -rf "$(IOS_TEST_RESULT)"
	rm -rf "$(IOS_TEST_RESULT).xcresult"
	xcodebuild \
		-project ios/DelightApp.xcodeproj \
		-scheme DelightApp \
		-configuration $(CONFIGURATION) \
		-sdk iphonesimulator \
		-destination "platform=iOS Simulator,name=$(SIMULATOR_DEVICE)" \
		-derivedDataPath "$(DERIVED_DATA)" \
		-resultBundlePath "$(IOS_TEST_RESULT)" \
		-enableCodeCoverage YES \
		test

test:
	(cd cli && go test $(GO_TEST_ARGS) $(CLI_TEST_PKGS))
	(cd webclient && go test $(GO_TEST_ARGS) $(WEBCLIENT_TEST_PKGS))
	(cd server && go test $(GO_TEST_ARGS) $(SERVER_TEST_PKGS))

.PHONY: lint

lint:
	@mkdir -p "$(BUNDLED_GOCACHE)" "$(BUNDLED_GOMODCACHE)" "$(BUNDLED_GOLANGCI_CACHE)"
	(cd shared && GOCACHE="$(BUNDLED_GOCACHE)" GOMODCACHE="$(BUNDLED_GOMODCACHE)" GOLANGCI_LINT_CACHE="$(BUNDLED_GOLANGCI_CACHE)" golangci-lint run ./...)
	(cd cli && GOCACHE="$(BUNDLED_GOCACHE)" GOMODCACHE="$(BUNDLED_GOMODCACHE)" GOLANGCI_LINT_CACHE="$(BUNDLED_GOLANGCI_CACHE)" golangci-lint run ./...)
	(cd server && GOCACHE="$(BUNDLED_GOCACHE)" GOMODCACHE="$(BUNDLED_GOMODCACHE)" GOLANGCI_LINT_CACHE="$(BUNDLED_GOLANGCI_CACHE)" golangci-lint run ./...)

.PHONY: docker-build

docker-build:
	docker build -f server/Dockerfile -t "$(SERVER_DOCKER_IMAGE)" .

.PHONY: cli server webclient

cli:
	$(MAKE) -C cli

server:
	$(MAKE) -C server

webclient:
	$(MAKE) -C webclient
