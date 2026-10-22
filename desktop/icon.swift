// Apache-2.0. Native vector rendering of the existing sidebar F + left-bar mark.
import AppKit
import Foundation
let destination = CommandLine.arguments[1]
try FileManager.default.createDirectory(atPath: destination, withIntermediateDirectories: true)
for size in [16, 32, 128, 256, 512] {
  for scale in [1, 2] {
    let pixels = size * scale
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: pixels, pixelsHigh: pixels, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    let factor = CGFloat(pixels) / 1024
    let transform = NSAffineTransform(); transform.scale(by: factor); transform.concat()
    NSColor(calibratedRed: 0.97, green: 0.965, blue: 0.949, alpha: 1).setFill()
    NSBezierPath(roundedRect: NSRect(x: 32, y: 32, width: 960, height: 960), xRadius: 205, yRadius: 205).fill()
    NSColor(calibratedRed: 23/255, green: 27/255, blue: 34/255, alpha: 1).setFill()
    NSBezierPath(rect: NSRect(x: 240, y: 240, width: 78, height: 544)).fill()
    let attributes: [NSAttributedString.Key: Any] = [.font: NSFont.systemFont(ofSize: 650, weight: .black), .foregroundColor: NSColor(calibratedRed: 23/255, green: 27/255, blue: 34/255, alpha: 1)]
    ("F" as NSString).draw(at: NSPoint(x: 339, y: 100), withAttributes: attributes)
    NSGraphicsContext.restoreGraphicsState()
    let suffix = scale == 2 ? "@2x" : ""
    let data = bitmap.representation(using: .png, properties: [:])!
    try data.write(to: URL(fileURLWithPath: destination + "/icon_\(size)x\(size)\(suffix).png"))
  }
}
