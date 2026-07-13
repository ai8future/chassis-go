# Seal malformed envelope panic

`seal.Decrypt` passed attacker-controlled nonce lengths to AES-GCM, which can panic. The decoder now validates salt, nonce, and tag lengths before key derivation or GCM open, with unit and fuzz regressions ensuring malformed envelopes return `ErrDecrypt`.
