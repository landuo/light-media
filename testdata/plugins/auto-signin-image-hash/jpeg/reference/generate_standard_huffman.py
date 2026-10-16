"""Offline table transcription from the fixed vendored jstdhuff.c."""
import re,pathlib
p=pathlib.Path(__file__).parent
s=(p/'src_jstdhuff.c').read_text();tables={}
for n in ('bits_dc_luminance','val_dc_luminance','bits_ac_luminance','val_ac_luminance','bits_dc_chrominance','val_dc_chrominance','bits_ac_chrominance','val_ac_chrominance'):
 text=re.search(r'\b'+n+r'\[[^]]*\]\s*=\s*\{([^}]+)\}',s).group(1);text=re.sub(r'/\*.*?\*/','',text,flags=re.S);tables[n]=[int(v,0) for v in text.split(',') if v.strip()]
out='''// Standard Huffman tables transcribed from libjpeg-turbo 3.1.4.1
// src/jstdhuff.c, fixed commit in reference/source-checksums.json.
// Copyright (C) 1991-1998, Thomas G. Lane.
// Copyright (C) 2013, 2022, 2024, D. R. Commander.
// Modified/transcribed to Go in 2026. IJG notice: ../reference/README.ijg.
package jpeg

func (d *decoder) defaultHuffman() error {
'''
for i,kind in ((0,'luminance'),(1,'chrominance')):
 for tc in ('dc','ac'):
  n=tables['bits_'+tc+'_'+kind][1:];v=tables['val_'+tc+'_'+kind]
  out+=' if h:=&d.huff['+tc+'Table]['+str(i)+'];h.nCodes==0 {\n h.nCodes='+str(len(v))+'\n copy(h.vals[:],[]byte{'+','.join(str(x) for x in v)+'})\n if err:=h.derive([maxCodeLength]int32{'+','.join(str(x) for x in n)+'});err!=nil{return err}\n }\n'
out+=' return nil\n}\n';(p.parent/'jpegport/standard_huffman.go').write_text(out)
