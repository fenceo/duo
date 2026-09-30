//go:build windows && amd64

package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Never open a window or send physical input in the user's desktop. The CI
// runner is disposable and this helper is the only target authorized by the test.
func TestDesktopNativeIsolatedWindow(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("DUO_TEST_DESKTOP_NATIVE") != "1" {
		t.Skip("disposable CI Windows desktop only")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "Fixture.cs")
	binary := filepath.Join(dir, "Fixture.exe")
	code := `using System;using System.IO;using System.Drawing;using System.Windows.Forms;using System.Web.Script.Serialization;
class Fixture {
[STAThread] static void Main(string[] args){
 Application.EnableVisualStyles();var form=new Form();form.Text="Duo isolated desktop fixture";form.StartPosition=FormStartPosition.Manual;form.Location=new Point(60,60);form.ClientSize=new Size(420,220);form.FormBorderStyle=FormBorderStyle.FixedDialog;form.TopMost=true;
 var text=new TextBox();text.Location=new Point(25,25);text.Size=new Size(350,30);form.Controls.Add(text);
 var button=new Button();button.Text="Verify fixture";button.Location=new Point(25,100);button.Size=new Size(150,40);form.Controls.Add(button);
 button.Click+=(s,e)=>{File.WriteAllText(Path.Combine(args[0],"result.txt"),text.Text);};
 form.Shown+=(s,e)=>{form.Activate();text.Focus();var p=button.PointToScreen(new Point(40,20));File.WriteAllText(Path.Combine(args[0],"ready.json"),new JavaScriptSerializer().Serialize(new {x=p.X-form.Left,y=p.Y-form.Top}));};
 var timer=new Timer();timer.Interval=60000;timer.Tick+=(s,e)=>form.Close();timer.Start();Application.Run(form);
}}`
	if err := os.WriteFile(source, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	compiler := filepath.Join(os.Getenv("WINDIR"), "Microsoft.NET", "Framework64", "v4.0.30319", "csc.exe")
	build := exec.Command(compiler, "/nologo", "/target:winexe", "/platform:x64", "/out:"+binary, "/reference:System.Windows.Forms.dll", "/reference:System.Drawing.dll", "/reference:System.Web.Extensions.dll", source)
	hideCommand(build)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	cmd := exec.Command(binary, dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	var point struct{ X, Y int }
	target := ""
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(dir, "ready.json"))
		if err == nil && json.Unmarshal(raw, &point) == nil {
			targets, err := nativeDesktopTargets()
			if err == nil {
				for _, item := range targets {
					if item.Title == "Duo isolated desktop fixture" {
						target = item.ID
					}
				}
			}
			if target != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if target == "" {
		t.Fatal("isolated fixture did not open an interactive window")
	}
	observe := func() desktopObservation {
		t.Helper()
		var out desktopObservation
		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			out, err = nativeDesktopCapture(target)
			if err == nil {
				return out
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal(err)
		return out
	}
	frame := observe()
	img, err := png.Decode(bytes.NewReader(frame.Data))
	if err != nil || img.Bounds().Dx() < 400 {
		t.Fatal("native screenshot failed", err)
	}
	if err = nativeDesktopInput(frame, DesktopAction{Action: "type", Text: "duo-fixture"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if err = nativeDesktopInput(frame, DesktopAction{Action: "type", Text: "must-not-be-sent"}); err == nil {
		t.Fatal("AI input failed to reject changed physical input")
	}
	frame = observe()
	if err = nativeDesktopInput(frame, DesktopAction{Action: "key", Key: "end"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	frame.Human = true
	if err = nativeDesktopInput(frame, DesktopAction{Action: "type", Text: "-manual"}); err != nil {
		t.Fatal("manual input incorrectly treated as AI", err)
	}
	time.Sleep(150 * time.Millisecond)
	frame = observe()
	if err = nativeDesktopInput(frame, DesktopAction{Action: "click", X: point.X, Y: point.Y}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(dir, "result.txt"))
		if err == nil {
			if strings.TrimSpace(string(raw)) != "duo-fixture-manual" {
				t.Fatal("native input mismatch", string(raw))
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("synthetic button did not receive native click")
}
