import AVFoundation
import SwiftUI

struct QRScannerView: UIViewControllerRepresentable {
    final class Coordinator: NSObject, AVCaptureMetadataOutputObjectsDelegate {
        var parent: QRScannerView

        init(parent: QRScannerView) {
            self.parent = parent
        }

        func metadataOutput(_ output: AVCaptureMetadataOutput,
                            didOutput metadataObjects: [AVMetadataObject],
                            from connection: AVCaptureConnection) {
            guard let object = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
                  object.type == .qr,
                  let value = object.stringValue else {
                return
            }
            parent.onScan(value)
        }
    }

    let onScan: (String) -> Void

    func makeCoordinator() -> Coordinator {
        Coordinator(parent: self)
    }

    func makeUIViewController(context: Context) -> ScannerViewController {
        let controller = ScannerViewController()
        controller.onScan = onScan
        return controller
    }

    func updateUIViewController(_ uiViewController: ScannerViewController, context: Context) {
    }
}

final class ScannerViewController: UIViewController {
    var onScan: ((String) -> Void)?
    private let session = AVCaptureSession()
    private let metadataOutput = AVCaptureMetadataOutput()

    override func viewDidLoad() {
        super.viewDidLoad()

        guard let videoDevice = AVCaptureDevice.default(for: .video),
              let videoInput = try? AVCaptureDeviceInput(device: videoDevice),
              session.canAddInput(videoInput) else {
            return
        }
        session.addInput(videoInput)

        if session.canAddOutput(metadataOutput) {
            session.addOutput(metadataOutput)
            metadataOutput.setMetadataObjectsDelegate(self, queue: DispatchQueue.main)
            metadataOutput.metadataObjectTypes = [.qr]
        }

        let previewLayer = AVCaptureVideoPreviewLayer(session: session)
        previewLayer.videoGravity = .resizeAspectFill
        previewLayer.frame = view.layer.bounds
        view.layer.addSublayer(previewLayer)
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        session.startRunning()
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        session.stopRunning()
    }
}

extension ScannerViewController: AVCaptureMetadataOutputObjectsDelegate {
    func metadataOutput(_ output: AVCaptureMetadataOutput,
                        didOutput metadataObjects: [AVMetadataObject],
                        from connection: AVCaptureConnection) {
        guard let object = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
              object.type == .qr,
              let value = object.stringValue else {
            return
        }
        session.stopRunning()
        onScan?(value)
    }
}
