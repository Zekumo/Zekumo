// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "ZekumoConsole",
    defaultLocalization: "zh-Hans",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "ZekumoConsole", targets: ["ZekumoConsole"]),
        .library(name: "ZekumoKit", targets: ["ZekumoKit"]),
    ],
    targets: [
        .target(name: "ZekumoKit"),
        .executableTarget(
            name: "ZekumoConsole",
            dependencies: ["ZekumoKit"],
            resources: [.process("Resources")]
        ),
        .testTarget(name: "ZekumoKitTests", dependencies: ["ZekumoKit"]),
    ]
)
