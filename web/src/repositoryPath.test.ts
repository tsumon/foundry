import {describe,it,expect} from "vitest";
import {supportedRepositoryPath} from "./repositoryPath";
describe("repository path admission",()=>{
 it("accepts absolute POSIX and ordinary drive paths",()=>{
  for(const p of ["/Users/you/project",String.raw`C:\Users\用户\Source Folder`,"D:/source/project"])expect(supportedRepositoryPath(p)).toBe(true);
 });
 it("refuses Windows namespaces, ADS, devices and aliases",()=>{
  for(const p of ["relative",String.raw`\\server\share`,String.raw`\\?\C:\source`,"C:relative","C:/source:file","C:/CON.txt","C:/a.","C:/a ","C:/a/../b","C:/PROGRA~1/repo"])expect(supportedRepositoryPath(p)).toBe(false);
 });
 it("refuses control characters rejected by native repository inspection",()=>{
  for(const prefix of ["/Users/you/", "C:/source/"]){
   for(const code of [...Array.from({length:32},(_,i)=>i),...Array.from({length:33},(_,i)=>127+i)]){
    expect(supportedRepositoryPath(prefix+"before"+String.fromCharCode(code)+"after")).toBe(false);
   }
  }
 });
 it("keeps ordinary Unicode names, spaces and POSIX punctuation usable",()=>{
  for(const p of ["/Users/用户/项目 🚀", "/tmp/café", "/tmp/a:b?c", "/tmp/CON", "C:/用户/Source Folder/项目🚀"]){
   expect(supportedRepositoryPath(p)).toBe(true);
  }
 });
});
