// +build !noasm !appengine
// Code generated by asm2asm, DO NOT EDIT.

package avx2

import (
	`github.com/bytedance/sonic/loader`
)

const (
    _entry__skip_object = 480
)

const (
    _stack__skip_object = 112
)

const (
    _size__skip_object = 10664
)

var (
    _pcsp__skip_object = [][2]uint32{
        {0x1, 0},
        {0x6, 8},
        {0x8, 16},
        {0xa, 24},
        {0xc, 32},
        {0xd, 40},
        {0x11, 48},
        {0x27f5, 112},
        {0x27f6, 48},
        {0x27f8, 40},
        {0x27fa, 32},
        {0x27fc, 24},
        {0x27fe, 16},
        {0x27ff, 8},
        {0x2803, 0},
        {0x29a8, 112},
    }
)

var _cfunc_skip_object = []loader.CFunc{
    {"_skip_object_entry", 0,  _entry__skip_object, 0, nil},
    {"_skip_object", _entry__skip_object, _size__skip_object, _stack__skip_object, _pcsp__skip_object},
}