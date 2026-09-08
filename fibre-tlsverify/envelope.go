package tlsverify

import "encoding/binary"

// envelopePrefix is prepended to the length-delimited RawBytesMessage before it
// is signed. It equals cometbft types.RawBytesSignBytesPrefix.
const envelopePrefix = "COMET::RAW_BYTES::SIGN"

// rawBytesMessageSignBytes reproduces cometbft
// types.RawBytesMessageSignBytes(chainID, uniqueID, signBytes): the exact byte
// string a validator's consensus key signs when endorsing raw application bytes
// (here, the Fibre TLS binding payload).
//
// Wire layout:
//
//	"COMET::RAW_BYTES::SIGN" || uvarint(len(m)) || m
//
// where m is the protobuf encoding of
//
//	message RawBytesMessage {
//	  string chain_id   = 1;
//	  bytes  sign_bytes = 2;
//	  string unique_id  = 3;
//	}
//
// Protobuf emits fields in ascending tag order, each as a length-delimited
// (wire type 2) record: tag byte, uvarint length, value. The leading uvarint is
// protobuf's standard length-delimited framing of the whole message
// (protoio.MarshalDelimited).
//
// This is protocol framing, not cryptography. It is pinned byte-for-byte by the
// golden vectors: if this function is wrong, the "valid" case fails its
// signature check.
func rawBytesMessageSignBytes(chainID, uniqueID string, signBytes []byte) []byte {
	var m []byte
	m = appendLenDelimField(m, 1, []byte(chainID))
	m = appendLenDelimField(m, 2, signBytes)
	m = appendLenDelimField(m, 3, []byte(uniqueID))

	out := make([]byte, 0, len(envelopePrefix)+binary.MaxVarintLen64+len(m))
	out = append(out, envelopePrefix...)
	out = binary.AppendUvarint(out, uint64(len(m)))
	return append(out, m...)
}

// appendLenDelimField appends one protobuf length-delimited (wire type 2) field.
func appendLenDelimField(b []byte, fieldNum int, val []byte) []byte {
	b = binary.AppendUvarint(b, uint64(fieldNum)<<3|2)
	b = binary.AppendUvarint(b, uint64(len(val)))
	return append(b, val...)
}

// signInputBytes is the payload handed to rawBytesMessageSignBytes as
// signBytes: the domain-separation prefix followed by the raw BindingPayload
// DER. Matches celestia-app tlsid.signedBytes.
func signInputBytes(bindingPayloadDER []byte) []byte {
	out := make([]byte, 0, len(signPrefix)+len(bindingPayloadDER))
	out = append(out, signPrefix...)
	return append(out, bindingPayloadDER...)
}
