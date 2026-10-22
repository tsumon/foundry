import {describe,it,expect} from "vitest";
import {supportedRepositoryPath} from "./repositoryPath";
describe("repository path admission",()=>{
 it("accepts absolute POSIX and ordinary drive paths",()=>{
  for(const p of ["/Users/you/project",String.raw`C:\Users\用户\Source Folder`,"D:/source/project"])expect(supportedRepositoryPath(p)).toBe(true);
 });
 it("refuses Windows namespaces, ADS, devices and aliases",()=>{
  for(const p of ["relative",String.raw`\\server\share`,String.raw`\\?\C:\source`,"C:relative","C:/source:file","C:/CON.txt","C:/a.","C:/a ","C:/a/../b","C:/PROGRA~1/repo"])expect(supportedRepositoryPath(p)).toBe(false);
 });
});
