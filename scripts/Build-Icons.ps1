[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
# The SVG is the single editable source. Raster outputs have transparent corners,
# are supersampled for small tray sizes, and require only Windows .NET Framework.
Add-Type -ReferencedAssemblies System.Drawing,System.Xml -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Imaging;
using System.Globalization;
using System.IO;
using System.Text.RegularExpressions;
using System.Xml;

public static class DuoIconAssets {
    static float Number(string s) { return Single.Parse(s, CultureInfo.InvariantCulture); }
    static float Attr(XmlElement e,string key) { return Number(e.GetAttribute(key)); }
    static GraphicsPath Shape(XmlElement e) {
        GraphicsPath p=new GraphicsPath();
        if(e.LocalName=="rect") {
            float x=Attr(e,"x"),y=Attr(e,"y"),w=Attr(e,"width"),h=Attr(e,"height"),d=2*Attr(e,"rx");
            p.AddArc(x,y,d,d,180,90);p.AddArc(x+w-d,y,d,d,270,90);
            p.AddArc(x+w-d,y+h-d,d,d,0,90);p.AddArc(x,y+h-d,d,d,90,90);p.CloseFigure();return p;
        }
        if(e.LocalName!="path")throw new InvalidDataException("Unsupported icon element");
        MatchCollection parts=Regex.Matches(e.GetAttribute("d"),@"[A-Za-z]|[-+]?(?:\d*\.?\d+)");
        float px=0,py=0;int i=0;
        while(i<parts.Count) {
            string op=parts[i++].Value;
            if(op=="M") { px=Number(parts[i++].Value);py=Number(parts[i++].Value);p.StartFigure(); }
            else if(op=="H") { float x=Number(parts[i++].Value);p.AddLine(px,py,x,py);px=x; }
            else if(op=="V") { float y=Number(parts[i++].Value);p.AddLine(px,py,px,y);py=y; }
            else if(op=="C") {
                float x1=Number(parts[i++].Value),y1=Number(parts[i++].Value),x2=Number(parts[i++].Value),y2=Number(parts[i++].Value),x=Number(parts[i++].Value),y=Number(parts[i++].Value);
                p.AddBezier(px,py,x1,y1,x2,y2,x,y);px=x;py=y;
            } else if(op=="Z")p.CloseFigure();else throw new InvalidDataException("Unsupported icon path command");
        }
        return p;
    }
    static Bitmap Render(XmlDocument svg,int size) {
        Bitmap result=new Bitmap(size,size,PixelFormat.Format32bppArgb);
        using(Bitmap large=new Bitmap(size*4,size*4,PixelFormat.Format32bppArgb)) {
            using(Graphics g=Graphics.FromImage(large)) {
                g.Clear(Color.Transparent);g.SmoothingMode=SmoothingMode.AntiAlias;g.ScaleTransform(size*4/256f,size*4/256f);
                foreach(XmlNode node in svg.DocumentElement.ChildNodes) {
                    XmlElement e=node as XmlElement;if(e==null)continue;
                    using(GraphicsPath p=Shape(e))using(Brush brush=new SolidBrush(ColorTranslator.FromHtml(e.GetAttribute("fill"))))g.FillPath(brush,p);
                }
            }
            using(Graphics g=Graphics.FromImage(result)) {
                g.CompositingMode=CompositingMode.SourceCopy;g.InterpolationMode=InterpolationMode.HighQualityBicubic;g.PixelOffsetMode=PixelOffsetMode.HighQuality;
                g.DrawImage(large,new Rectangle(0,0,size,size),0,0,large.Width,large.Height,GraphicsUnit.Pixel);
            }
        }
        return result;
    }
    static byte[] Frame(Bitmap bitmap) {
        using(MemoryStream stream=new MemoryStream()) {
            if(bitmap.Width==256) { bitmap.Save(stream,ImageFormat.Png);return stream.ToArray(); }
            using(BinaryWriter writer=new BinaryWriter(stream)) {
                int size=bitmap.Width,maskStride=((size+31)/32)*4;
                writer.Write(40);writer.Write(size);writer.Write(size*2);writer.Write((short)1);writer.Write((short)32);writer.Write(0);
                writer.Write(size*size*4+maskStride*size);writer.Write(0);writer.Write(0);writer.Write(0);writer.Write(0);
                for(int y=size-1;y>=0;y--)for(int x=0;x<size;x++) { Color c=bitmap.GetPixel(x,y);writer.Write(c.B);writer.Write(c.G);writer.Write(c.R);writer.Write(c.A); }
                for(int y=size-1;y>=0;y--) {
                    byte[] row=new byte[maskStride];for(int x=0;x<size;x++)if(bitmap.GetPixel(x,y).A<128)row[x/8]|=(byte)(128>>(x%8));writer.Write(row);
                }
                return stream.ToArray();
            }
        }
    }
    public static void Build(string source,string output) {
        XmlDocument svg=new XmlDocument();svg.XmlResolver=null;svg.Load(source);
        if(svg.DocumentElement.GetAttribute("viewBox")!="0 0 256 256")throw new InvalidDataException("Expected 256-unit icon canvas");
        int[] sizes={16,20,24,32,40,48,64,128,256};List<byte[]> frames=new List<byte[]>();
        foreach(int size in sizes)using(Bitmap bitmap=Render(svg,size))frames.Add(Frame(bitmap));
        using(BinaryWriter writer=new BinaryWriter(File.Create(Path.Combine(output,"favicon.ico")))) {
            writer.Write((short)0);writer.Write((short)1);writer.Write((short)sizes.Length);int offset=6+16*sizes.Length;
            for(int i=0;i<sizes.Length;i++) {
                writer.Write((byte)(sizes[i]==256?0:sizes[i]));writer.Write((byte)(sizes[i]==256?0:sizes[i]));writer.Write((byte)0);writer.Write((byte)0);writer.Write((short)1);writer.Write((short)32);
                writer.Write(frames[i].Length);writer.Write(offset);offset+=frames[i].Length;
            }
            foreach(byte[] frame in frames)writer.Write(frame);
        }
        using(Bitmap bitmap=Render(svg,256))bitmap.Save(Path.Combine(output,"icon-256.png"),ImageFormat.Png);
        using(Bitmap bitmap=Render(svg,180))bitmap.Save(Path.Combine(output,"apple-touch-icon.png"),ImageFormat.Png);
    }
}
'@
[DuoIconAssets]::Build((Join-Path $root 'web\icon.svg'),(Join-Path $root 'web'))
Write-Output 'Generated Duo ICO (16-256 px), PNG and touch icon from web/icon.svg.'
