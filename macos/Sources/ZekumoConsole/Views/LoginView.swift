import SwiftUI
import ZekumoKit

struct LoginView: View {
    @Environment(AppModel.self) private var model
    @State private var password = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        @Bindable var model = model
        HStack(spacing: 0) {
            VStack(spacing: 16) {
                Mascot.welcome.image.resizable().scaledToFit().frame(width: 220)
                Text("Zekumo").font(.system(size: 34, weight: .bold, design: .rounded))
                Text("小游戏后端控制台").foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color.accentColor.opacity(0.08))

            VStack(alignment: .leading, spacing: 18) {
                Text("登录控制台").font(.title.weight(.semibold))
                Form {
                    TextField("服务器", text: $model.serverText, prompt: Text("http://localhost:8080"))
                    TextField("用户名", text: $model.username)
                        .textContentType(.username)
                    SecureField("密码", text: $password)
                        .textContentType(.password)
                        .onSubmit(submit)
                }
                .formStyle(.columns)
                .disabled(busy)

                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.callout)
                }

                HStack {
                    Spacer()
                    if busy { ProgressView().controlSize(.small) }
                    Button("登录", action: submit)
                        .keyboardShortcut(.defaultAction)
                        .controlSize(.large)
                        .disabled(busy || password.isEmpty || model.username.isEmpty)
                }
            }
            .padding(40)
            .frame(width: 420)
        }
    }

    private func submit() {
        guard !busy, !password.isEmpty else { return }
        busy = true
        error = nil
        Task {
            defer { busy = false }
            do {
                try await model.login(password: password)
                password = ""
            } catch let err as ZekumoError {
                if case .server(401, _, _) = err {
                    error = "用户名或密码错误"
                } else {
                    error = err.localizedDescription
                }
            } catch {
                self.error = "无法连接服务器:\(error.localizedDescription)"
            }
        }
    }
}
