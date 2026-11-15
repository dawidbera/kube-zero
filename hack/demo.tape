# VHS tape script for generating KubeZero terminal demo GIF
# Requirements: VHS (https://github.com/charmbracelet/vhs)
# Run: vhs hack/demo.tape

Output docs/images/demo.gif

Set FontSize 15
Set Width 960
Set Height 540
Set Theme "Catppuccin Mocha"
Set WindowBar Colorful
Set Padding 20
Set TypingSpeed 50ms

# Hidden initialization
Hide
Type "source ./hack/demo-mock.sh && clear"
Enter
Sleep 500ms
Show

# Step 1: Active workload
Type "# Step 1: Active workload running with desired replicas"
Enter
Sleep 1s
Type "kubectl get deployment sample-app"
Enter
Sleep 2500ms

# Step 2: Scheduled sleep / inactivity trigger
Type "# Step 2: ZeroPolicy sleep schedule scales deployment to 0"
Enter
Sleep 1s
Type "kubectl get deployment sample-app"
Enter
Sleep 2500ms

# Step 3: Incoming HTTP traffic triggers Wake-on-Traffic
Type "# Step 3: Inbound HTTP request triggers Wake-Proxy cold start"
Enter
Sleep 1s
Type "curl -i http://sample-app:8080/api/orders"
Enter
Sleep 3s

# Step 4: Revived deployment
Type "# Step 4: Transparently served with revived healthy pod"
Enter
Sleep 1s
Type "kubectl get pods -l app=sample-app"
Enter
Sleep 3s
