package apply

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// hwpUnitsPerMM is HWPUNIT (1/7200 inch) per millimetre.
const hwpUnitsPerMM = 7200 / 25.4

// pxUnits is HWPUNIT per pixel at 96 DPI, the unit of hp:imgDim.
const pxUnits = 75

type newImage struct {
	id   string // manifest item id, also binaryItemIDRef
	path string // zip entry name
	data []byte
}

// addImage reads the PNG at src, registers it as a new BinData item and
// returns the hp:pic element that shows it width_mm wide. The file is
// stored as it is: no re-encoding, no resampling.
func (ed *editor) addImage(sec *section, src string, widthMM float64) (string, error) {
	if !filepath.IsAbs(src) && ed.opt.BaseDir != "" {
		src = filepath.Join(ed.opt.BaseDir, src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("%s: not a PNG: %w", src, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("%s: empty image", src)
	}
	ed.nextImage++
	id := fmt.Sprintf("image%d", ed.nextImage)
	ed.images = append(ed.images, newImage{id: id, path: "BinData/" + id + ".png", data: data})

	w := int(math.Round(widthMM * hwpUnitsPerMM))
	h := int(math.Round(float64(w) * float64(cfg.Height) / float64(cfg.Width)))
	ed.nextObj++
	ed.nextZ++
	return picXML(sec.pfx, sec.core, ed.nextObj, ed.nextZ, id, w, h, cfg, filepath.Base(src)), nil
}

// picXML is an inline (treatAsChar) picture in the form Hancom Office
// writes for an inserted image.
func picXML(p, c string, id int64, z int, binID string, w, h int, cfg image.Config, name string) string {
	dw, dh := cfg.Width*pxUnits, cfg.Height*pxUnits
	return fmt.Sprintf(`<%[1]s:pic id="%[3]d" zOrder="%[4]d" numberingType="PICTURE" textWrap="TOP_AND_BOTTOM" textFlow="BOTH_SIDES" lock="0" dropcapstyle="None" href="" groupLevel="0" instid="%[3]d" reverse="0">`+
		`<%[1]s:offset x="0" y="0"/><%[1]s:orgSz width="%[6]d" height="%[7]d"/><%[1]s:curSz width="0" height="0"/><%[1]s:flip horizontal="0" vertical="0"/>`+
		`<%[1]s:rotationInfo angle="0" centerX="%[8]d" centerY="%[9]d" rotateimage="1"/>`+
		`<%[1]s:renderingInfo><%[2]s:transMatrix e1="1" e2="0" e3="0" e4="0" e5="1" e6="0"/><%[2]s:scaMatrix e1="1" e2="0" e3="0" e4="0" e5="1" e6="0"/><%[2]s:rotMatrix e1="1" e2="0" e3="0" e4="0" e5="1" e6="0"/></%[1]s:renderingInfo>`+
		`<%[2]s:img binaryItemIDRef="%[5]s" bright="0" contrast="0" effect="REAL_PIC" alpha="0"/>`+
		`<%[1]s:imgRect><%[2]s:pt0 x="0" y="0"/><%[2]s:pt1 x="%[6]d" y="0"/><%[2]s:pt2 x="%[6]d" y="%[7]d"/><%[2]s:pt3 x="0" y="%[7]d"/></%[1]s:imgRect>`+
		`<%[1]s:imgClip left="0" right="%[10]d" top="0" bottom="%[11]d"/><%[1]s:inMargin left="0" right="0" top="0" bottom="0"/>`+
		`<%[1]s:imgDim dimwidth="%[10]d" dimheight="%[11]d"/><%[1]s:effects/>`+
		`<%[1]s:sz width="%[6]d" widthRelTo="ABSOLUTE" height="%[7]d" heightRelTo="ABSOLUTE" protect="0"/>`+
		`<%[1]s:pos treatAsChar="1" affectLSpacing="0" flowWithText="1" allowOverlap="0" holdAnchorAndSO="0" vertRelTo="PARA" horzRelTo="COLUMN" vertAlign="TOP" horzAlign="LEFT" vertOffset="0" horzOffset="0"/>`+
		`<%[1]s:outMargin left="0" right="0" top="0" bottom="0"/>`+
		`<%[1]s:shapeComment>%[12]s</%[1]s:shapeComment></%[1]s:pic>`,
		p, c, id, z, binID, w, h, w/2, h/2, dw, dh,
		escText(fmt.Sprintf("그림입니다.\n원본 그림의 이름: %s\n원본 그림의 크기: 가로 %dpixel, 세로 %dpixel", name, cfg.Width, cfg.Height)))
}
