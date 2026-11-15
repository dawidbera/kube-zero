# Mock environment for VHS recording
export PS1="➜ \[\033[01;32m\]dev-cluster\[\033[00m\]:\[\033[01;34m\]default\[\033[00m\]$ "
export KUBEZERO_STATE=1

kubectl() {
  if [ "$1" = "get" ] && [ "$2" = "deployment" ]; then
    if [ "${KUBEZERO_STATE}" = "1" ]; then
      echo "NAME         READY   UP-TO-DATE   AVAILABLE   AGE"
      echo "sample-app   2/2     2            2           42m"
      export KUBEZERO_STATE=0
    elif [ "${KUBEZERO_STATE}" = "0" ]; then
      echo "NAME         READY   UP-TO-DATE   AVAILABLE   AGE"
      echo "sample-app   0/0     0            0           55m"
      export KUBEZERO_STATE=2
    else
      echo "NAME         READY   UP-TO-DATE   AVAILABLE   AGE"
      echo "sample-app   1/1     1            1           2s"
    fi
  elif [ "$1" = "get" ] && [ "$2" = "pods" ]; then
    echo "NAME                          READY   STATUS    RESTARTS   AGE"
    echo "sample-app-7b89f5bc4-k9z2m    1/1     Running   0          3s"
  else
    command kubectl "$@" 2>/dev/null || true
  fi
}

curl() {
  sleep 0.8
  echo "HTTP/1.1 200 OK"
  echo "Content-Type: text/plain; charset=utf-8"
  echo "X-KubeZero-Cold-Start: true"
  echo ""
  echo "Hello from behind KubeZero Wake-Proxy!"
}
