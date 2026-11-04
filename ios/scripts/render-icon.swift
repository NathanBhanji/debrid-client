import CoreGraphics
import ImageIO
import Foundation

// Original vector mark: the web wordmark's graphite letter and orange full stop.
let root = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
let output = root.appendingPathComponent("Debrid/AppIcons.xcassets/AppIcon.appiconset")
let size = 1024
guard let context = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: size * 4,
    space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else { fatalError("Cannot create icon renderer") }
context.setFillColor(CGColor(red: 0.9529, green: 0.9451, blue: 0.9216, alpha: 1))
context.fill(CGRect(x: 0, y: 0, width: size, height: size))
let mark = CGMutablePath()
mark.move(to: CGPoint(x: 270, y: 236)); mark.addLine(to: CGPoint(x: 478, y: 236))
mark.addCurve(to: CGPoint(x: 774, y: 512), control1: CGPoint(x: 672, y: 236), control2: CGPoint(x: 774, y: 342))
mark.addCurve(to: CGPoint(x: 478, y: 788), control1: CGPoint(x: 774, y: 682), control2: CGPoint(x: 672, y: 788))
mark.addLine(to: CGPoint(x: 270, y: 788)); mark.closeSubpath()
mark.move(to: CGPoint(x: 394, y: 354)); mark.addLine(to: CGPoint(x: 478, y: 354))
mark.addCurve(to: CGPoint(x: 646, y: 512), control1: CGPoint(x: 590, y: 354), control2: CGPoint(x: 646, y: 412))
mark.addCurve(to: CGPoint(x: 478, y: 670), control1: CGPoint(x: 646, y: 612), control2: CGPoint(x: 590, y: 670))
mark.addLine(to: CGPoint(x: 394, y: 670)); mark.closeSubpath()
context.addPath(mark); context.setFillColor(CGColor(red: 0.1373, green: 0.1412, blue: 0.1216, alpha: 1)); context.drawPath(using: .eoFill)
context.setFillColor(CGColor(red: 1, green: 0.31, blue: 0, alpha: 1))
context.fillEllipse(in: CGRect(x: 697, y: 180, width: 142, height: 142))
guard let image = context.makeImage(), let destination = CGImageDestinationCreateWithURL(output.appendingPathComponent("AppIcon.png") as CFURL, "public.png" as CFString, 1, nil) else { fatalError("Cannot encode icon") }
CGImageDestinationAddImage(destination, image, nil)
guard CGImageDestinationFinalize(destination) else { fatalError("Cannot write icon") }
print("Rendered original opaque1024px Debrid icon")
