package main

import (
 "encoding/json"
 "os"
 "testing"
)

func TestReleaseVersionMatchesPackages(t *testing.T) {
 for _,path:=range []string{"package.json","package-lock.json"} {
  raw,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
  var metadata struct{Version string `json:"version"`}
  if json.Unmarshal(raw,&metadata)!=nil || metadata.Version!=version {t.Fatalf("%s version %q differs from packaged service %q",path,metadata.Version,version)}
 }
}
