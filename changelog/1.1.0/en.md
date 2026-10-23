# SPK MM Client 1.1.0

- Build with Go 1.26.10, including the crypto/x509 certificate-trust security update (GO-2026-6809).
- Dictate Russian messages in channel and thread composers through a local SPK LAIHub with GigaAM.
- Recognized text is added to your editable draft and saved normally. Sending remains a separate action; audio is not attached to the message or saved by the client.
- Record for up to 60 seconds, finish early or cancel. Leaving the conversation, hiding the application or starting dictation elsewhere releases the microphone and ignores late recognition results.
- Show microphone-access and recognition errors without clearing the draft. macOS packages include the microphone permission explanation.

Dictation requires SPK LAIHub running with its speech API on loopback port 8095 and the GigaAM model available. The client does not install models or start LAIHub. See the dedicated dictation documentation for configuration and verification limits.

Native Linux, Windows and macOS packages for amd64/arm64 and SHA-256 checksums remain available. Signing and runtime requirements are unchanged.
