# seal empty secret and passphrase guards

`seal.Sign` accepted an empty HMAC secret and encryption helpers accepted empty passphrases, making accidental unset secret configuration easy to miss.

Fixed by panicking on empty signing secrets, returning false for empty-secret verification, and rejecting empty encryption/decryption passphrases.
