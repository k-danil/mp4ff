package hevc

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Eyevinn/mp4ff/bits"
)

// Errors for parsing and handling HEVC slices
var (
	ErrNoSliceHeader      = errors.New("No slice header")
	ErrInvalidSliceType   = errors.New("Invalid slice type")
	ErrTooFewBytesToParse = errors.New("Too few bytes to parse symbol")
)

// SliceType - HEVC slice type
type SliceType uint

func (s SliceType) String() string {
	switch s {
	case SLICE_I:
		return "I"
	case SLICE_P:
		return "P"
	case SLICE_B:
		return "B"
	default:
		return ""
	}
}

// HEVC slice types
const (
	SLICE_P = SliceType(0)
	SLICE_B = SliceType(1)
	SLICE_I = SliceType(2)
)

type SliceHeader struct {
	SliceType                         SliceType
	FirstSliceSegmentInPicFlag        bool
	NoOutputOfPriorPicsFlag           bool
	PicParameterSetId                 uint32
	DependentSliceSegmentFlag         bool
	SegmentAddress                    uint
	PicOutputFlag                     bool
	ColourPlaneId                     uint8
	PicOrderCntLsb                    uint16
	ShortTermRefPicSetSpsFlag         bool
	ShortTermRefPicSet                *ShortTermRPS
	ShortTermRefPicSetIdx             byte
	NumLongTermSps                    uint
	NumLongTermPics                   uint
	LongTermRefPicSets                []LongTermRPS
	TemporalMvpEnabledFlag            bool
	SaoLumaFlag                       bool
	SaoChromaFlag                     bool
	NumRefIdxActiveOverrideFlag       bool
	NumRefIdxActiveMinus1             [2]uint8
	RefPicListsModification           *RefPicListsModification
	MvdL1ZeroFlag                     bool
	CabacInitFlag                     bool
	CollocatedFromL0Flag              bool
	CollocatedRefIdx                  uint
	PredWeightTable                   *PredWeightTable
	FiveMinusMaxNumMergeCand          uint
	UseIntegerMvFlag                  bool
	QpDelta                           bool
	CbQpOffset                        int
	CrQpOffset                        int
	ActYQpOffset                      int
	ActCbQpOffset                     int
	ActCrQpOffset                     int
	CuChromaQpOffsetEnabledFlag       bool
	DeblockingFilterOverrideFlag      bool
	DeblockingFilterDisabledFlag      bool
	BetaOffsetDiv2                    int
	TcOffsetDiv2                      int
	LoopFilterAcrossSlicesEnabledFlag bool
	NumEntryPointOffsets              uint
	OffsetLenMinus1                   uint8
	EntryPointOffsetMinus1            []uint32
	SegmentHeaderExtensionLength      uint
	SegmentHeaderExtensionDataByte    []byte
	Size                              uint32
}

type LongTermRPS struct {
	PocLsbLt               uint
	UsedByCurrPicLtFlag    bool
	DeltaPocMsbPresentFlag bool
	DeltaPocMsbCycleLt     uint
}

type RefPicListsModification struct {
	RefPicListModificationFlagL0 bool
	ListEntryL0                  []uint8
	RefPicListModificationFlagL1 bool
	ListEntryL1                  []uint8
}

type PredWeightTable struct {
	LumaLog2WeightDenom        uint
	DeltaChromaLog2WeightDenom int
	WeightsL0                  []Weight
	WeightsL1                  []Weight
}

type Weight struct {
	LumaWeightFlag    bool
	ChromaWeightFlag  bool
	DeltaLumaWeight   int
	LumaOffset        int
	DeltaChromaWeight [2]int
	DeltaChromaOffset [2]int
}

func ParseSliceHeader(nalu []byte, spsMap map[uint32]*SPS, ppsMap map[uint32]*PPS) (*SliceHeader, error) {
	sh := &SliceHeader{}

	buf := bytes.NewBuffer(nalu)
	r := bits.NewAccErrEBSPReader(buf)
	naluHdrBits := r.Read(8)
	naluType := GetNaluType(byte(naluHdrBits>>1) & 0x3f)
	sh.FirstSliceSegmentInPicFlag = r.ReadFlag()
	if naluType >= NALU_BLA_W_LP && naluType <= NALU_IRAP_VCL23 {
		sh.NoOutputOfPriorPicsFlag = r.ReadFlag()
	}
	sh.PicParameterSetId = uint32(r.ReadExpGolomb())
	pps, ok := ppsMap[sh.PicParameterSetId]
	if !ok {
		return nil, fmt.Errorf("pps ID %d unknown", sh.PicParameterSetId)
	}

	var sps *SPS
	sps, ok = spsMap[pps.SeqParameterSetID]
	if !ok {
		return nil, fmt.Errorf("sps ID %d unknown", pps.SeqParameterSetID)
	}

	if !sh.FirstSliceSegmentInPicFlag {
		if pps.DependentSliceSegmentsEnabledFlag {
			sh.DependentSliceSegmentFlag = r.ReadFlag()
		}
		CtbLog2SizeY := sps.Log2MinLumaCodingBlockSizeMinus3 + 3 + sps.Log2DiffMaxMinLumaCodingBlockSize
		CtbSizeY := uint(1 << CtbLog2SizeY)
		PicHeightInCtbsY := ceilDiv(uint(sps.PicHeightInLumaSamples), CtbSizeY)
		PicWidthInCtbsY := ceilDiv(uint(sps.PicWidthInLumaSamples), CtbSizeY)
		PicSizeInCtbsY := PicWidthInCtbsY * PicHeightInCtbsY
		sh.SegmentAddress = r.Read(ceilLog2(PicSizeInCtbsY))
	}

	/*
			NumPicTotalCurr = 0
			if( nal_unit_type != IDR_W_RADL && nal_unit_type != IDR_N_LP ) {
			for( i = 0; i < NumNegativePics[ CurrRpsIdx ]; i++ ) if( UsedByCurrPicS0[ CurrRpsIdx ][ i ] )
			NumPicTotalCurr++
			for( i = 0; i < NumPositivePics[ CurrRpsIdx ]; i++ ) if( UsedByCurrPicS1[ CurrRpsIdx ][ i ] )
		    NumPicTotalCurr++
			for( i = 0; i < num_long_term_sps + num_long_term_pics; i++ ) if( UsedByCurrPicLt[ i ] )
			NumPicTotalCurr++
			}
			if( pps_curr_pic_ref_enabled_flag )
			NumPicTotalCurr++
			NumPicTotalCurr += NumActiveRefLayerPics
	*/

	if !sh.DependentSliceSegmentFlag {
		var NumPicTotalCurr uint8

		// The variable ChromaArrayType is derived as equal to 0 when separate_colour_plane_flag is equal to 1
		// and chroma_format_idc is equal to 3.
		ChromaArrayType := sps.ChromaFormatIDC
		if sps.SeparateColourPlaneFlag && sps.ChromaFormatIDC == 3 {
			ChromaArrayType = 0
		}

		// Decoders shall ignore the presence and value of slice_reserved_flag[ i ]
		for i := uint8(0); i < pps.NumExtraSliceHeaderBits; i++ {
			_ = r.ReadFlag()
		}
		sh.SliceType = SliceType(r.ReadExpGolomb())
		if pps.OutputFlagPresentFlag {
			sh.PicOutputFlag = r.ReadFlag()
		}
		if sps.SeparateColourPlaneFlag {
			sh.ColourPlaneId = uint8(r.Read(2))
		}
		if naluType != NALU_IDR_W_RADL && naluType != NALU_IDR_N_LP {
			// value of log2_max_pic_order_cnt_lsb_minus4 shall be in the range of 0 to 12, inclusive
			sh.PicOrderCntLsb = uint16(r.Read(int(sps.Log2MaxPicOrderCntLsbMinus4 + 4)))
			sh.ShortTermRefPicSetSpsFlag = r.ReadFlag()

			// fix this ugly shit
			if !sh.ShortTermRefPicSetSpsFlag {
				// errors?
				strps := parseShortTermRPS(r, sps.NumShortTermRefPicSets,
					sps.NumShortTermRefPicSets, sps)
				sh.ShortTermRefPicSet = &strps
			} else if sps.NumShortTermRefPicSets > 1 {
				sh.ShortTermRefPicSetIdx = byte(r.Read(ceilLog2(uint(sps.NumShortTermRefPicSets))))
				sh.ShortTermRefPicSet = &sps.ShortTermRefPicSets[sh.ShortTermRefPicSetIdx]
			}
			NumPicTotalCurr += sh.ShortTermRefPicSet.CountNegAndPosPics()

			if sps.LongTermRefPicsPresentFlag {
				if sps.NumLongTermRefPics > 0 {
					sh.NumLongTermSps = r.ReadExpGolomb()
				}
				sh.NumLongTermPics = r.ReadExpGolomb()
				for i := uint(0); i < sh.NumLongTermSps+sh.NumLongTermPics; i++ {
					lt := LongTermRPS{}
					if i < sh.NumLongTermSps {
						if sps.NumLongTermRefPics > 1 {
							LtIdxSps := r.Read(ceilLog2(sps.NumLongTermRefPics))
							lt = sps.LongTermRefPicSets[LtIdxSps]
						}
					} else {
						lt.PocLsbLt = r.Read(int(sps.Log2MaxPicOrderCntLsbMinus4 + 4))
						lt.UsedByCurrPicLtFlag = r.ReadFlag()
					}
					if lt.UsedByCurrPicLtFlag {
						NumPicTotalCurr++
					}
					lt.DeltaPocMsbPresentFlag = r.ReadFlag()
					if lt.DeltaPocMsbPresentFlag {
						lt.DeltaPocMsbCycleLt = r.ReadExpGolomb()
					}
					sh.LongTermRefPicSets = append(sh.LongTermRefPicSets, lt)
				}
			}
			if sps.SpsTemporalMvpEnabledFlag {
				sh.TemporalMvpEnabledFlag = r.ReadFlag()
			}
		}
		if sps.SampleAdaptiveOffsetEnabledFlag {
			sh.SaoLumaFlag = r.ReadFlag()
			if ChromaArrayType != 0 {
				sh.SaoChromaFlag = r.ReadFlag()
			}
		}
		if sh.SliceType == SLICE_P || sh.SliceType == SLICE_B {
			sh.NumRefIdxActiveOverrideFlag = r.ReadFlag()
			// When the current slice is a P or B slice and num_ref_idx_l0_active_minus1 is not present,
			// num_ref_idx_l0_active_minus1 is inferred to be equal to num_ref_idx_l0_default_active_minus1.
			sh.NumRefIdxActiveMinus1 = pps.NumRefIdxDefaultActiveMinus1
			if sh.NumRefIdxActiveOverrideFlag {
				// value shall be in the range of 0 to 14, inclusive
				sh.NumRefIdxActiveMinus1[0] = uint8(r.ReadExpGolomb())
				if sh.SliceType == SLICE_B {
					sh.NumRefIdxActiveMinus1[1] = uint8(r.ReadExpGolomb())
				}
			}

			if pps.ListsModificationPresentFlag {
				if pps.SccExtension != nil && pps.SccExtension.CurrPicRefEnabledFlag {
					NumPicTotalCurr++
				}
				if NumPicTotalCurr > 1 {
					var err error
					sh.RefPicListsModification, err = parseRefPicListsModification(
						r, sh.SliceType, sh.NumRefIdxActiveMinus1, NumPicTotalCurr)
					if err != nil {
						return sh, err
					}
				}
			}
			if sh.SliceType == SLICE_B {
				sh.MvdL1ZeroFlag = r.ReadFlag()
			}
			if pps.CabacInitPresentFlag {
				sh.CabacInitFlag = r.ReadFlag()
			}
			if sh.TemporalMvpEnabledFlag {
				if sh.SliceType == SLICE_B {
					sh.CollocatedFromL0Flag = r.ReadFlag()
				}
				if (sh.CollocatedFromL0Flag && sh.NumRefIdxActiveMinus1[0] > 0) ||
					(!sh.CollocatedFromL0Flag && sh.NumRefIdxActiveMinus1[1] > 0) {
					sh.CollocatedRefIdx = r.ReadExpGolomb()
				}
			}
			if (pps.WeightedPredFlag && sh.SliceType == SLICE_P) ||
				(pps.WeightedBipredFlag && sh.SliceType == SLICE_B) {
				var err error
				// fix chromaArrayType
				sh.PredWeightTable, err = parsePredWeightTable(r, sh.SliceType, sh.NumRefIdxActiveMinus1, ChromaArrayType)
				if err != nil {
					return sh, err
				}
			}
			sh.FiveMinusMaxNumMergeCand = r.ReadExpGolomb()
			if sps.SccExtension != nil && sps.SccExtension.MotionVectorResolutionControlIdc == 2 {
				sh.UseIntegerMvFlag = r.ReadFlag()
			}
		}
		sh.QpDelta = r.ReadFlag()
		if pps.SliceChromaQpOffsetsPresentFlag {
			sh.CbQpOffset = r.ReadSignedGolomb()
			sh.CrQpOffset = r.ReadSignedGolomb()
		}
		if pps.SccExtension != nil && pps.SccExtension.SliceActQpOffsetsPresentFlag {
			sh.ActYQpOffset = r.ReadSignedGolomb()
			sh.ActCbQpOffset = r.ReadSignedGolomb()
			sh.ActCrQpOffset = r.ReadSignedGolomb()
		}
		if pps.RangeExtension != nil && pps.RangeExtension.ChromaQpOffsetListEnabledFlag {
			sh.CuChromaQpOffsetEnabledFlag = r.ReadFlag()
		}
		if pps.DeblockingFilterOverrideEnabledFlag {
			sh.DeblockingFilterOverrideFlag = r.ReadFlag()
		}
		if sh.DeblockingFilterOverrideFlag {
			sh.DeblockingFilterDisabledFlag = r.ReadFlag()
			{
				if !sh.DeblockingFilterDisabledFlag {
					sh.BetaOffsetDiv2 = r.ReadSignedGolomb()
					sh.TcOffsetDiv2 = r.ReadSignedGolomb()
				}
			}
		}
		if pps.LoopFilterAcrossSlicesEnabledFlag &&
			(sh.SaoLumaFlag || sh.SaoChromaFlag || !sh.DeblockingFilterDisabledFlag) {
			sh.LoopFilterAcrossSlicesEnabledFlag = r.ReadFlag()
		}
	}
	if pps.TilesEnabledFlag || pps.EntropyCodingSyncEnabledFlag {
		sh.NumEntryPointOffsets = r.ReadExpGolomb()
		if sh.NumEntryPointOffsets > 0 {
			// value shall be in the range of 0 to 31, inclusive
			sh.OffsetLenMinus1 = uint8(r.ReadExpGolomb())
			for i := uint(0); i < sh.NumEntryPointOffsets; i++ {
				sh.EntryPointOffsetMinus1 = append(sh.EntryPointOffsetMinus1, uint32(r.Read(int(sh.OffsetLenMinus1+1))))
			}
		}
	}
	if pps.SliceSegmentHeaderExtensionPresentFlag {
		sh.SegmentHeaderExtensionLength = r.ReadExpGolomb()
		for i := uint(0); i < sh.SegmentHeaderExtensionLength; i++ {
			sh.SegmentHeaderExtensionDataByte = append(sh.SegmentHeaderExtensionDataByte, byte(r.Read(8)))
		}
	}

	if r.AccError() != nil {
		return nil, r.AccError()
	}

	/* compute the size in bytes. Round up if not an integral number of bytes .*/
	sh.Size = uint32(r.NrBytesRead())
	if r.NrBitsReadInCurrentByte() > 0 {
		sh.Size++
	}

	return sh, nil
}

func parseRefPicListsModification(r *bits.AccErrEBSPReader, sliceType SliceType,
	refIdx [2]uint8, numPicTotalCurr uint8) (*RefPicListsModification, error) {
	rplm := &RefPicListsModification{
		RefPicListModificationFlagL0: r.ReadFlag(),
	}
	if rplm.RefPicListModificationFlagL0 {
		for i := uint8(0); i <= refIdx[0]; i++ {
			rplm.ListEntryL0 = append(rplm.ListEntryL0, uint8(r.Read(ceilLog2(uint(numPicTotalCurr)))))
		}
	}
	if sliceType == SLICE_B {
		if rplm.RefPicListModificationFlagL1 {
			for i := uint8(0); i <= refIdx[1]; i++ {
				rplm.ListEntryL1 = append(rplm.ListEntryL1, uint8(r.Read(ceilLog2(uint(numPicTotalCurr)))))
			}
		}
	}

	if r.AccError() != nil {
		return nil, r.AccError()
	}

	return rplm, nil
}

func parsePredWeightTable(r *bits.AccErrEBSPReader, sliceType SliceType,
	refIdx [2]uint8, chromaArrayType byte) (*PredWeightTable, error) {
	pwt := &PredWeightTable{
		LumaLog2WeightDenom: r.ReadExpGolomb(),
	}
	if chromaArrayType != 0 {
		pwt.DeltaChromaLog2WeightDenom = r.ReadSignedGolomb()
	}

	pwt.WeightsL0 = make([]Weight, refIdx[0]+1)
	for i := uint8(0); i <= refIdx[0]; i++ {
		// if( ( pic_layer_id( RefPicList0[ i ] ) != nuh_layer_id ) | |
		//( PicOrderCnt( RefPicList0[ i ] ) != PicOrderCnt( CurrPic ) ) )
		pwt.WeightsL0[i].LumaWeightFlag = r.ReadFlag()
	}
	if chromaArrayType != 0 {
		for i := uint8(0); i <= refIdx[0]; i++ {
			// if( ( pic_layer_id( RefPicList0[ i ] ) != nuh_layer_id ) | |
			//( PicOrderCnt( RefPicList0[ i ] ) != PicOrderCnt( CurrPic ) ) )
			pwt.WeightsL0[i].ChromaWeightFlag = r.ReadFlag()
		}
	}
	for i := uint8(0); i <= refIdx[0]; i++ {
		if pwt.WeightsL0[i].LumaWeightFlag {
			pwt.WeightsL0[i].DeltaLumaWeight = r.ReadSignedGolomb()
			pwt.WeightsL0[i].LumaOffset = r.ReadSignedGolomb()
		}
		if pwt.WeightsL0[i].ChromaWeightFlag {
			for j := 0; j < 2; j++ {
				pwt.WeightsL0[i].DeltaChromaWeight[j] = r.ReadSignedGolomb()
				pwt.WeightsL0[i].DeltaChromaOffset[j] = r.ReadSignedGolomb()
			}
		}
	}
	if sliceType == SLICE_B {
		pwt.WeightsL1 = make([]Weight, refIdx[1]+1)
		for i := uint8(0); i <= refIdx[1]; i++ {
			// if( ( pic_layer_id( RefPicList0[ i ] ) != nuh_layer_id ) | |
			//( PicOrderCnt( RefPicList0[ i ] ) != PicOrderCnt( CurrPic ) ) )
			pwt.WeightsL1[i].LumaWeightFlag = r.ReadFlag()
		}
		if chromaArrayType != 0 {
			for i := uint8(0); i <= refIdx[1]; i++ {
				// if( ( pic_layer_id( RefPicList0[ i ] ) != nuh_layer_id ) | |
				//( PicOrderCnt( RefPicList0[ i ] ) != PicOrderCnt( CurrPic ) ) )
				pwt.WeightsL1[i].ChromaWeightFlag = r.ReadFlag()
			}
		}
		for i := uint8(0); i <= refIdx[1]; i++ {
			if pwt.WeightsL1[i].LumaWeightFlag {
				pwt.WeightsL1[i].DeltaLumaWeight = r.ReadSignedGolomb()
				pwt.WeightsL1[i].LumaOffset = r.ReadSignedGolomb()
			}
			if pwt.WeightsL1[i].ChromaWeightFlag {
				for j := 0; j < 2; j++ {
					pwt.WeightsL1[i].DeltaChromaWeight[j] = r.ReadSignedGolomb()
					pwt.WeightsL1[i].DeltaChromaOffset[j] = r.ReadSignedGolomb()
				}
			}
		}
	}

	if r.AccError() != nil {
		return nil, r.AccError()
	}

	return pwt, nil
}

func ceilDiv(a, b uint) uint {
	return (a + b - 1) / b
}
