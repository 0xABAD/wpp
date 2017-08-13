(function () {
    let foo = 'foo bar baz';
    function exclaim(str) {
        return str + '!';
    }
    window.addEventListener("load", evt => {
        let h2 = document.createElement('h2');
        h2.textContent = exclaim(foo);
        document.body.appendChild(h2);
    });
})();
