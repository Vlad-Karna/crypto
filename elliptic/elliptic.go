// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package elliptic implements several standard elliptic curves over prime
// fields.
package elliptic

// This package operates, internally, on Jacobian coordinates. For a given
// (x, y) position on the curve, the Jacobian coordinates are (x1, y1, z1)
// where x = x1/z1² and y = y1/z1³. The greatest speedups come when the whole
// calculation can be performed within the transform (as in ScalarMult and
// ScalarBaseMult). But even for Add and Double, it's faster to apply and
// reverse the transform than to operate in affine coordinates.

import (
	"io"
	"math/big"
	"sync"
	stdelliptic "crypto/elliptic"
)

// A Curve represents a short-form Weierstrass curve.
// See https://www.hyperelliptic.org/EFD/g1p/auto-shortw.html
type Curve interface {
	// Params returns the parameters for the curve.
	Params() *CurveParams
	// IsOnCurve reports whether the given (x,y) lies on the curve.
	IsOnCurve(x, y *big.Int) bool
	// Add returns the sum of (x1,y1) and (x2,y2)
	Add(x1, y1, x2, y2 *big.Int) (x, y *big.Int)
	// Double returns 2*(x,y)
	Double(x1, y1 *big.Int) (x, y *big.Int)
	// ScalarMult returns k*(Bx,By) where k is a number in big-endian form.
	ScalarMult(x1, y1 *big.Int, k []byte) (x, y *big.Int)
	// ScalarBaseMult returns k*G, where G is the base point of the group
	// and k is an integer in big-endian form.
	ScalarBaseMult(k []byte) (x, y *big.Int)
}

// CurveParams contains the parameters of an elliptic curve and also provides
// a generic, non-constant time implementation of Curve.
type CurveParams struct {
	stdelliptic.CurveParams
/*	P       *big.Int // the order of the underlying field
	N       *big.Int // the order of the base point
	B       *big.Int // the constant of the curve equation
	Gx, Gy  *big.Int // (x,y) of the base point
	BitSize int      // the size of the underlying field
	Name    string   // the canonical name of the curve*/
	A       *big.Int // the coefficient of x in the curve equation
}

// Params returns the elliptic CurveParams of the implemented curve.
func (curve *CurveParams) Params() *CurveParams {
	return curve
}

// IsOnCurve returns whether or not a (x, y) point is on the curve.
func (curve *CurveParams) IsOnCurve(x, y *big.Int) bool {
	// y² = x³ + ax + b
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, curve.P)

	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)

	aX := new(big.Int).Mul(curve.A, x)
	aX.Mod(aX, curve.P)

	x3.Add(x3, aX)
	x3.Add(x3, curve.B)
	x3.Mod(x3, curve.P)

	return x3.Cmp(y2) == 0
}

// zForAffine returns a Jacobian Z value for the affine point (x, y). If x and
// y are zero, it assumes that they represent the point at infinity because (0,
// 0) is not on the any of the curves handled here.
func zForAffine(x, y *big.Int) *big.Int {
	z := new(big.Int)
	if x.Sign() != 0 || y.Sign() != 0 {
		z.SetInt64(1)
	}
	return z
}

// affineFromJacobian reverses the Jacobian transform. See the comment at the
// top of the file. If the point is ∞ it returns 0, 0.
func (curve *CurveParams) affineFromJacobian(x, y, z *big.Int) (xOut, yOut *big.Int) {
	if z.Sign() == 0 {
		return new(big.Int), new(big.Int)
	}

	zinv := new(big.Int).ModInverse(z, curve.P)
	zinvsq := new(big.Int).Mul(zinv, zinv)

	xOut = new(big.Int).Mul(x, zinvsq)
	xOut.Mod(xOut, curve.P)
	zinvsq.Mul(zinvsq, zinv)
	yOut = new(big.Int).Mul(y, zinvsq)
	yOut.Mod(yOut, curve.P)
	return
}

// Add returns the sum of two affine points.
func (curve *CurveParams) Add(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	z1 := zForAffine(x1, y1)
	z2 := zForAffine(x2, y2)
	return curve.affineFromJacobian(curve.addJacobian(x1, y1, z1, x2, y2, z2))
}

// addJacobian takes two points in Jacobian coordinates, (x1, y1, z1) and
// (x2, y2, z2) and returns their sum, also in Jacobian form.
func (curve *CurveParams) addJacobian(x1, y1, z1, x2, y2, z2 *big.Int) (*big.Int, *big.Int, *big.Int) {
	// See http://www.hyperelliptic.org/EFD/g1p/auto-shortw-jacobian.html#addition-add-2007-bl
	x3, y3, z3 := new(big.Int), new(big.Int), new(big.Int)
	if z1.Sign() == 0 {
		x3.Set(x2)
		y3.Set(y2)
		z3.Set(z2)
		return x3, y3, z3
	}
	if z2.Sign() == 0 {
		x3.Set(x1)
		y3.Set(y1)
		z3.Set(z1)
		return x3, y3, z3
	}

	z1z1 := new(big.Int).Mul(z1, z1)
	z1z1.Mod(z1z1, curve.P)
	z2z2 := new(big.Int).Mul(z2, z2)
	z2z2.Mod(z2z2, curve.P)

	u1 := new(big.Int).Mul(x1, z2z2)
	u1.Mod(u1, curve.P)
	u2 := new(big.Int).Mul(x2, z1z1)
	u2.Mod(u2, curve.P)
	h := new(big.Int).Sub(u2, u1)
	xEqual := h.Sign() == 0
	if h.Sign() == -1 {
		h.Add(h, curve.P)
	}
	i := new(big.Int).Lsh(h, 1)
	i.Mul(i, i)
	j := new(big.Int).Mul(h, i)

	s1 := new(big.Int).Mul(y1, z2)
	s1.Mul(s1, z2z2)
	s1.Mod(s1, curve.P)
	s2 := new(big.Int).Mul(y2, z1)
	s2.Mul(s2, z1z1)
	s2.Mod(s2, curve.P)
	r := new(big.Int).Sub(s2, s1)
	if r.Sign() == -1 {
		r.Add(r, curve.P)
	}
	yEqual := r.Sign() == 0
	if xEqual && yEqual {
		return curve.doubleJacobian(x1, y1, z1)
	}
	r.Lsh(r, 1)
	v := new(big.Int).Mul(u1, i)

	x3.Set(r)
	x3.Mul(x3, x3)
	x3.Sub(x3, j)
	x3.Sub(x3, v)
	x3.Sub(x3, v)
	x3.Mod(x3, curve.P)

	y3.Set(r)
	v.Sub(v, x3)
	y3.Mul(y3, v)
	s1.Mul(s1, j)
	s1.Lsh(s1, 1)
	y3.Sub(y3, s1)
	y3.Mod(y3, curve.P)

	z3.Add(z1, z2)
	z3.Mul(z3, z3)
	z3.Sub(z3, z1z1)
	z3.Sub(z3, z2z2)
	z3.Mul(z3, h)
	z3.Mod(z3, curve.P)

	return x3, y3, z3
}

// Double returns the double of an affine point.
func (curve *CurveParams) Double(x1, y1 *big.Int) (*big.Int, *big.Int) {
	z1 := zForAffine(x1, y1)
	return curve.affineFromJacobian(curve.doubleJacobian(x1, y1, z1))
}

// doubleJacobian takes a point in Jacobian coordinates, (x, y, z), and
// returns its double, also in Jacobian form.
func (curve *CurveParams) doubleJacobian(x, y, z *big.Int) (*big.Int, *big.Int, *big.Int) {
	// See http://www.hyperelliptic.org/EFD/g1p/auto-shortw-jacobian.html#doubling-dbl-2007-bl
	xx := new(big.Int).Mul(x, x)
	xx.Mod(xx, curve.P)

	yy := new(big.Int).Mul(y, y)
	yy.Mod(yy, curve.P)

	yyyy := new(big.Int).Mul(yy, yy)
	yyyy.Mod(yyyy, curve.P)

	zz := new(big.Int).Mul(z, z)
	zz.Mod(zz, curve.P)

	s := new(big.Int).Add(x, yy)
	s.Mul(s, s)
	s.Sub(s, xx)
	if s.Sign() == -1 {
		s.Add(s, curve.P)
	}
	s.Sub(s, yyyy)
	if s.Sign() == -1 {
		s.Add(s, curve.P)
	}
	s.Lsh(s, 1)
	s.Mod(s, curve.P)

	m := new(big.Int).Lsh(xx, 1)
	m.Add(m, xx)
	m2 := new(big.Int).Mul(zz, zz)
	m2.Mul(curve.A, m2)
	m.Add(m, m2)
	m.Mod(m, curve.P)

	t := new(big.Int).Mul(m, m)
	t2 := new(big.Int).Lsh(s, 1)
	t.Sub(t, t2)
	if t.Sign() == -1 {
		t.Add(t, curve.P)
	}
	t.Mod(t, curve.P)

	x3 := new(big.Int).Set(t)

	y3 := new(big.Int).Sub(s, t)
	if y3.Sign() == -1 {
		y3.Add(y3, curve.P)
	}
	y3.Mul(y3, m)
	yyyy8 := new(big.Int).Lsh(yyyy, 3)
	y3.Sub(y3, yyyy8)
	if y3.Sign() == -1 {
		y3.Add(y3, curve.P)
	}
	y3.Mod(y3, curve.P)

	z3 := new(big.Int).Add(y, z)
	z3.Mul(z3, z3)
	z3.Sub(z3, yy)
	if z3.Sign() == -1 {
		z3.Add(z3, curve.P)
	}
	z3.Sub(z3, zz)
	if z3.Sign() == -1 {
		z3.Add(z3, curve.P)
	}
	z3.Mod(z3, curve.P)

	return x3, y3, z3
}

// ScalarMult multiplies the point (Bx, By) by the scalar k using repeated doubling.
func (curve *CurveParams) ScalarMult(Bx, By *big.Int, k []byte) (*big.Int, *big.Int) {
	Bz := new(big.Int).SetInt64(1)
	x, y, z := new(big.Int), new(big.Int), new(big.Int)

	for _, byte := range k {
		for bitNum := 0; bitNum < 8; bitNum++ {
			x, y, z = curve.doubleJacobian(x, y, z)
			if byte&0x80 == 0x80 {
				x, y, z = curve.addJacobian(Bx, By, Bz, x, y, z)
			}
			byte <<= 1
		}
	}

	return curve.affineFromJacobian(x, y, z)
}

// ScalarBaseMult multiplies the base point by the scalar k using repeated doubling.
func (curve *CurveParams) ScalarBaseMult(k []byte) (*big.Int, *big.Int) {
	return curve.ScalarMult(curve.Gx, curve.Gy, k)
}

var mask = []byte{0xff, 0x1, 0x3, 0x7, 0xf, 0x1f, 0x3f, 0x7f}

// GenerateKey returns a public/private key pair. The private key is
// generated using the given reader, which must return random data.
func GenerateKey(curve Curve, rand io.Reader) (priv []byte, x, y *big.Int, err error) {
	N := curve.Params().N
	bitSize := N.BitLen()
	byteLen := (bitSize + 7) >> 3
	priv = make([]byte, byteLen)

	for x == nil {
		_, err = io.ReadFull(rand, priv)
		if err != nil {
			return
		}
		// We have to mask off any excess bits in the case that the size of the
		// underlying field is not a whole number of bytes.
		priv[0] &= mask[bitSize%8]
		// This is because, in tests, rand will return all zeros and we don't
		// want to get the point at infinity and loop forever.
		priv[1] ^= 0x42

		// If the scalar is out of range, sample another random number.
		if new(big.Int).SetBytes(priv).Cmp(N) >= 0 {
			continue
		}

		x, y = curve.ScalarBaseMult(priv)
	}
	return
}

// Marshal converts a point into the uncompressed form specified in section 4.3.6 of ANSI X9.62.
func Marshal(curve Curve, x, y *big.Int) []byte {
	byteLen := (curve.Params().BitSize + 7) >> 3

	ret := make([]byte, 1+2*byteLen)
	ret[0] = 4 // uncompressed point

	xBytes := x.Bytes()
	copy(ret[1+byteLen-len(xBytes):], xBytes)
	yBytes := y.Bytes()
	copy(ret[1+2*byteLen-len(yBytes):], yBytes)
	return ret
}

// MarshalCompressed converts a point on the curve into the compressed form
// specified in section 4.3.6 of ANSI X9.62.
func MarshalCompressed(curve Curve, x, y *big.Int) []byte {
	byteLen := (curve.Params().BitSize + 7) / 8
	compressed := make([]byte, 1+byteLen)
	compressed[0] = byte(y.Bit(0)) | 2
	xBytes := x.Bytes()
	copy(compressed[1+byteLen-len(xBytes):], xBytes)
	return compressed
}

// Unmarshal converts a point, serialized by Marshal, into an x, y pair.
// It is an error if the point is not in uncompressed form or is not on the curve.
// On error, x = nil.
func Unmarshal(curve Curve, data []byte) (x, y *big.Int) {
	byteLen := (curve.Params().BitSize + 7) >> 3
	if len(data) != 1+2*byteLen {
		return
	}
	if data[0] != 4 { // uncompressed form
		return
	}
	p := curve.Params().P
	x = new(big.Int).SetBytes(data[1 : 1+byteLen])
	y = new(big.Int).SetBytes(data[1+byteLen:])
	if x.Cmp(p) >= 0 || y.Cmp(p) >= 0 {
		return nil, nil
	}
	if !curve.IsOnCurve(x, y) {
		return nil, nil
	}
	return
}

// UnmarshalCompressed converts a point, serialized by MarshalCompressed, into an x, y pair.
// It is an error if the point is not in compressed form or is not on the curve.
// On error, x = nil.
func UnmarshalCompressed(curve Curve, data []byte) (x, y *big.Int) {
	byteLen := (curve.Params().BitSize + 7) / 8
	if len(data) != 1+byteLen {
		return nil, nil
	}
	if data[0] != 2 && data[0] != 3 { // compressed form
		return nil, nil
	}
	p := curve.Params().P
	x = new(big.Int).SetBytes(data[1:])
	if x.Cmp(p) >= 0 {
		return nil, nil
	}
	// y² = x³ + ax + b
	// y = curve.Params().polynomial(x)
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)

	aX := new(big.Int).Mul(curve.Params().A, x)
	aX.Mod(aX, p)

	x3.Add(x3, aX)

	x3.Add(x3, curve.Params().B)

	x3.Mod(x3, p)

	y = x3.ModSqrt(x3, p)

	if y == nil {
		return nil, nil
	}
	if byte(y.Bit(0)) != data[0]&1 {
		y.Neg(y).Mod(y, p)
	}
	if !curve.IsOnCurve(x, y) {
		return nil, nil
	}
	return
}

var initonce sync.Once
var p384 *CurveParams
var p521 *CurveParams
var secp256k1 *CurveParams

func initAll() {
	initP224()
//	initP256()
	initP384()
	initP521()
	initSecp256k1()
}

func initP384() {
	// See FIPS 186-3, section D.2.4
	p384 = &CurveParams{}
	p384.Name = "P-384"
	p384.P, _ = new(big.Int).SetString("39402006196394479212279040100143613805079739270465446667948293404245721771496870329047266088258938001861606973112319", 10)
	p384.N, _ = new(big.Int).SetString("39402006196394479212279040100143613805079739270465446667946905279627659399113263569398956308152294913554433653942643", 10)
	p384.A, _ = new(big.Int).SetString("fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffeffffffff0000000000000000fffffffc", 16)
	p384.B, _ = new(big.Int).SetString("b3312fa7e23ee7e4988e056be3f82d19181d9c6efe8141120314088f5013875ac656398d8a2ed19d2a85c8edd3ec2aef", 16)
	p384.Gx, _ = new(big.Int).SetString("aa87ca22be8b05378eb1c71ef320ad746e1d3b628ba79b9859f741e082542a385502f25dbf55296c3a545e3872760ab7", 16)
	p384.Gy, _ = new(big.Int).SetString("3617de4a96262c6f5d9e98bf9292dc29f8f41dbd289a147ce9da3113b5f0b8c00a60b1ce1d7e819d7a431d7c90ea0e5f", 16)
	p384.BitSize = 384
}

func initP521() {
	// See FIPS 186-3, section D.2.5
	p521 = &CurveParams{}
	p521.Name = "P-521"
	p521.P, _ = new(big.Int).SetString("6864797660130609714981900799081393217269435300143305409394463459185543183397656052122559640661454554977296311391480858037121987999716643812574028291115057151", 10)
	p521.N, _ = new(big.Int).SetString("6864797660130609714981900799081393217269435300143305409394463459185543183397655394245057746333217197532963996371363321113864768612440380340372808892707005449", 10)
	p521.A, _ = new(big.Int).SetString("01fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffc", 16)
	p521.B, _ = new(big.Int).SetString("051953eb9618e1c9a1f929a21a0b68540eea2da725b99b315f3b8b489918ef109e156193951ec7e937b1652c0bd3bb1bf073573df883d2c34f1ef451fd46b503f00", 16)
	p521.Gx, _ = new(big.Int).SetString("c6858e06b70404e9cd9e3ecb662395b4429c648139053fb521f828af606b4d3dbaa14b5e77efe75928fe1dc127a2ffa8de3348b3c1856a429bf97e7e31c2e5bd66", 16)
	p521.Gy, _ = new(big.Int).SetString("11839296a789a3bc0045c8a5fb42c7d1bd998f54449579b446817afbd17273e662c97ee72995ef42640c550b9013fad0761353c7086a272c24088be94769fd16650", 16)
	p521.BitSize = 521
}

func initSecp256k1() {
	secp256k1 = &CurveParams{}
	secp256k1.Name = "secp256k1"
	secp256k1.P, _ = new(big.Int).SetString("fffffffffffffffffffffffffffffffffffffffffffffffffffffffefffffc2f", 16)
	secp256k1.N, _ = new(big.Int).SetString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141", 16)
	secp256k1.A = new(big.Int)
	secp256k1.B = new(big.Int).SetInt64(7)
	secp256k1.Gx, _ = new(big.Int).SetString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798", 16)
	secp256k1.Gy, _ = new(big.Int).SetString("483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8", 16)
	secp256k1.BitSize = 256
}

// P256 returns a Curve which implements P-256 (see FIPS 186-3, section D.2.3)
//
// The cryptographic operations are implemented using constant-time algorithms.
//func P256() Curve {
//	return stdelliptic.P256()
//	initonce.Do(initAll)
//	return p256
//}

// P384 returns a Curve which implements P-384 (see FIPS 186-3, section D.2.4)
//
// The cryptographic operations do not use constant-time algorithms.
func P384() Curve {
	initonce.Do(initAll)
	return p384
}

// P521 returns a Curve which implements P-521 (see FIPS 186-3, section D.2.5)
//
// The cryptographic operations do not use constant-time algorithms.
func P521() Curve {
	initonce.Do(initAll)
	return p521
}

// Secp256k1 returns a Curve which implements secp256k1 (see SEC 2, section 2.4.1)
//
// The cryptographic operations do not use constant-time algorithms.
func Secp256k1() Curve {
	initonce.Do(initAll)
	return secp256k1
}
